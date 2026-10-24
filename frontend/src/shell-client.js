import { HybridError, normalizeError } from "./sdk-core.js";

/** 将绑定返回的 Base64 字节还原为 Uint8Array，供流式 TextDecoder 按实际编码处理跨块字符。 */
function bytes(value) { return Uint8Array.from(atob(value || ""), (character) => character.charCodeAt(0)); }

/** 创建命令客户端，保留底层任务接口，同时提供带输出回调和 AbortSignal 的 run 便捷接口。 */
export function createShell(service, invoke) {
  /** 启动后台命令；程序、参数或脚本只作为数据传给 Go，不在浏览器执行 eval。 */
  async function start(options, callOptions) { return invoke(() => service.Start(options), callOptions); }
  /** 按字节游标读取新增输出，返回的 stdout/stderr 为 Base64；手动任务可自行选择解码器。 */
  async function read(id, stdoutOffset = 0, stderrOffset = 0) { return invoke(() => service.Read(id, stdoutOffset, stderrOffset)); }
  /** 取消任务及受管理子进程，等待终态请继续调用 read。 */
  async function cancel(id) { return invoke(() => service.Cancel(id)); }
  /** 清理已结束任务的输出记录，不删除命令产生的文件。 */
  async function remove(id) { return invoke(() => service.Remove(id)); }

  /** 执行命令并逐块回调输出；正常终态返回状态、退出码和完整保留文本，外部取消或回调异常会请求停止进程。 */
  async function run(options, { signal, onStdout, onStderr, encoding = "utf-8", keepHistory = false } = {}) {
    for (const callback of [onStdout, onStderr]) if (callback !== undefined && typeof callback !== "function") throw new HybridError("E_INVALID_ARGUMENT", "输出回调必须是函数");
    const out = new TextDecoder(encoding);
    const err = new TextDecoder(encoding);
    if (signal?.aborted) throw new HybridError("E_CANCELLED", "命令等待已取消");
    // 启动 RPC 先取得任务编号，再处理取消，避免信号抢先结束等待而无法确定需要停止哪个进程。
    const job = await start(options);
    let stdoutOffset = 0, stderrOffset = 0, stdout = "", stderr = "", ended = false;
    try {
      while (true) {
        if (signal?.aborted) throw new HybridError("E_CANCELLED", "命令等待已取消");
        const value = await invoke(() => service.Read(job.id, stdoutOffset, stderrOffset), { signal });
        stdoutOffset = value.stdoutOffset; stderrOffset = value.stderrOffset;
        const done = value.job.state !== "running";
        const stdoutChunk = out.decode(bytes(value.stdout), { stream: !done });
        const stderrChunk = err.decode(bytes(value.stderr), { stream: !done });
        stdout += stdoutChunk; stderr += stderrChunk;
        if (stdoutChunk) await onStdout?.(stdoutChunk);
        if (stderrChunk) await onStderr?.(stderrChunk);
        if (done) {
          ended = true;
          const result = { ...value.job, stdout, stderr };
          if (!keepHistory) await remove(job.id);
          return result;
        }
        await new Promise((resolve) => setTimeout(resolve, 150));
      }
    } catch (error) {
      const value = normalizeError(error);
      value.jobId = job.id;
      throw value;
    } finally {
      if (!ended) {
        // 不复用已经取消的 signal，确保异常处理仍能把停止请求发送给宿主。
        const deadline = Date.now() + 3000;
        await invoke(() => service.Cancel(job.id), { timeoutMs: 1500 }).catch(() => {});
        if (!keepHistory) {
          // 给宿主最多三秒完成终止和管道收尾；失败时保留记录，调用者仍可使用错误中的 jobId 手动清理。
          while (Date.now() < deadline) {
            try {
              const last = await invoke(() => service.Read(job.id, 0, 0), { timeoutMs: Math.max(1, Math.min(1000, deadline - Date.now())) });
              if (last.job.state !== "running") {
                await invoke(() => service.Remove(job.id), { timeoutMs: Math.max(1, Math.min(1000, deadline - Date.now())) });
                break;
              }
            }
            catch { break; }
            await new Promise((resolve) => setTimeout(resolve, 50));
          }
        }
      }
    }
  }
  return { start, read, cancel, remove, run };
}
