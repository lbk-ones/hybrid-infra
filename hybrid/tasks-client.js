/** 创建任务客户端：函数留在当前 JS 页面执行，宿主只接收状态；页面刷新后不能自动重放函数。 */
export function createTasks(service) {
  const active = new Map();
  const retries = new Map();
  let disposed = false;

  /** 生成统一取消错误，业务函数可通过 signal 检查并主动停止自己的网络请求或循环。 */
  function cancelled() {
    return Object.assign(new Error("任务已取消或执行会话已结束"), { code: "E_CANCELLED" });
  }

  /** 等待下次轮询；使用短等待维持排队租约，避免把回调序列化传给 Go。 */
  function pause() {
    return new Promise((resolve) => setTimeout(resolve, 300));
  }

  /** 执行一个业务函数，report 返回最新任务；取消只发协作信号，函数返回前不会伪造已停止。 */
  async function run(title, operation, options = {}) {
    if (disposed) throw cancelled();
    if (typeof operation !== "function") throw new TypeError("operation 必须是函数");
    if (options.signal?.aborted) throw cancelled();
    const controller = new AbortController();
    let task,
      timer,
      polling = false;
    let failure;
    // 外部取消转成本页信号，并通知宿主；异步创建尚未返回时先保留信号，拿到 ID 后再取消。
    const abort = () => {
      controller.abort();
      if (task) void service.Cancel(task.id).catch(() => {});
    };
    options.signal?.addEventListener("abort", abort, { once: true });

    /** 同步任务租约和取消状态；用户回调抛错时也会进入统一失败清理。 */
    async function poll() {
      task = await service.Poll(task.id);
      if (["cancelling", "cancelled", "interrupted"].includes(task.state)) controller.abort();
      options.onProgress?.(task);
      return task;
    }
    try {
      task = await service.Create(title);
      active.set(task.id, abort);
      if (!disposed) retries.set(task.id, { title, operation });
      if (retries.size > 500) retries.delete(retries.keys().next().value);
      if (disposed || controller.signal.aborted) {
        controller.abort();
        await service.Cancel(task.id);
        throw cancelled();
      }
      while (task.state === "queued" || task.state === "ready") {
        await poll();
        if (controller.signal.aborted) throw cancelled();
        if (task.state === "queued" || task.state === "ready") await pause();
      }
      if (task.state !== "running") throw cancelled();
      // 心跳不重叠；通信失败时终止信号，由业务函数完成自己的资源清理。
      timer = setInterval(async () => {
        if (polling) return;
        polling = true;
        try {
          await poll();
        } catch (error) {
          failure = error;
          controller.abort();
        } finally {
          polling = false;
        }
      }, 700);
      const value = await operation({
        id: task.id,
        signal: controller.signal,
        /** 上报百分比和简短说明，进度 -1 表示暂时无法估算。 */
        report: async (progress, message = "") => {
          if (controller.signal.aborted) throw failure || cancelled();
          const current = await service.Report(task.id, progress, message);
          options.onProgress?.(current);
          return current;
        },
      });
      if (controller.signal.aborted) throw failure || cancelled();
      task = await service.Finish(task.id, "completed", "");
      if (task.state !== "completed") throw cancelled();
      retries.delete(task.id);
      return { task, value };
    } catch (error) {
      if (task) {
        if (controller.signal.aborted) await service.Cancel(task.id).catch(() => {});
        await service
          .Finish(
            task.id,
            controller.signal.aborted ? "cancelled" : "failed",
            String(error?.message || error).slice(0, 120),
          )
          .catch(() => {});
      }
      throw error;
    } finally {
      clearInterval(timer);
      options.signal?.removeEventListener("abort", abort);
      if (task) active.delete(task.id);
    }
  }

  /** 只判断当前页面是否仍保留业务重试函数；原生任务请同时看列表中的 canRetry。 */
  function canRetry(id) {
    return retries.has(id) && !active.has(id) && !disposed;
  }

  /** 同页业务失败可重新执行保留的函数；原生重试委托宿主重新校验能力与文件凭证。 */
  function retry(id, options = {}) {
    if (disposed) return Promise.reject(cancelled());
    if (active.has(id))
      return Promise.reject(
        Object.assign(new Error("任务仍在执行，请先等待结束"), { code: "E_BUSY" }),
      );
    const saved = retries.get(id);
    return saved ? run(saved.title, saved.operation, options) : service.Retry(id);
  }

  /** 页面卸载时取消本页业务，清除重试引用；宿主租约负责处理来不及送达的取消请求。 */
  function dispose() {
    disposed = true;
    for (const abort of active.values()) abort();
    retries.clear();
  }
  return { ...service, run, retry, canRetry, dispose };
}
