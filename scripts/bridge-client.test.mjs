import assert from "node:assert/strict";
import test from "node:test";
import { createEvents } from "../frontend/src/events-client.js";
import { createShell } from "../frontend/src/shell-client.js";
import { call, createClient } from "../frontend/src/sdk-core.js";

/** 构造具备可取消轮询的测试传输，消息逐条经过 JSON 拷贝，确保回调函数没有被当成 RPC 参数传入宿主。 */
function fakeBus() {
  let sequence = 0;
  const listeners = new Map(), requests = new Map(), sessions = new Map();
  const calls = [];
  /** 把消息放入会话队列并唤醒等待者，模拟宿主收到消息后让长轮询返回。 */
  function push(session, message) {
    const state = sessions.get(session);
    if (!state) return;
    state.queue.push(JSON.parse(JSON.stringify(message)));
    state.wait?.();
  }
  const service = {
    /** 打开独立测试会话，不使用原生窗口和真实网络。 */
    async Open() { const id = `session-${++sequence}`; sessions.set(id,{queue:[]}); return id; },
    /** 记录订阅参数，回调由客户端保存，因此这里收到的始终只是主题和类型。 */
    async Subscribe(session,topic,kind,global) { const id=`listener-${++sequence}`; calls.push({topic,kind,global}); listeners.set(id,{session,topic,kind}); return id; },
    /** 返回可取消的挂起 Promise，防止测试退出后留下 SDK 超时计时器。 */
    Poll(session) {
      const state=sessions.get(session);
      let cancel;
      const promise=new Promise((resolve,reject)=>{
        const flush=()=>{if(state.queue.length){state.wait=undefined;resolve(state.queue.splice(0));}};
        state.wait=flush;flush();cancel=()=>{state.wait=undefined;reject(Object.assign(new Error("cancelled"),{code:"E_CANCELLED"}));};
      });
      promise.cancel=cancel;return promise;
    },
    /** 只取消指定监听，不影响其他同主题回调。 */
    async Unsubscribe(_session,id) { listeners.delete(id); },
    /** 发送 JSON 通知，不传递函数参数。 */
    async Emit(topic,data) { let count=0;for(const [id,l] of listeners){if(l.kind==="event"&&l.topic===topic){push(l.session,{kind:"event",topic,listenerId:id,data,sender:"test-sender"});count++;}}return count; },
    /** 模拟请求关联，处理器 Promise 返回结果后再完成请求方的 Promise。 */
    Request(topic,data) {
      const match=[...listeners].find(([,l])=>l.kind==="handler"&&l.topic===topic);
      if(!match)return Promise.reject(new Error("E_NO_HANDLER: none"));
      const [id,l]=match,requestId=`request-${++sequence}`;
      return new Promise((resolve,reject)=>{requests.set(requestId,{resolve,reject});push(l.session,{kind:"request",topic,listenerId:id,requestId,data,sender:"requester"});});
    },
    /** 把响应解析成纯 JSON 后返回，请求处理异常使用统一错误码。 */
    async Respond(_session,_listener,id,data,failed) { const pending=requests.get(id);requests.delete(id);if(failed)pending.reject(new Error("E_HANDLER_ERROR: failed"));else pending.resolve(JSON.parse(JSON.stringify(data))); },
    /** 释放会话和监听，模拟页面离开。 */
    async Close(session) { sessions.delete(session);for(const [id,l]of listeners){if(l.session===session)listeners.delete(id);} },
  };
  return {service,calls,listeners,sessions};
}

/** 等待微任务和回调完成；测试用短轮询不代表生产中的事件传输方式。 */
async function eventually(predicate) { const end=Date.now()+1000;while(!predicate()){if(Date.now()>end)throw new Error("condition not reached");await new Promise(resolve=>setTimeout(resolve,2));} }

// 验证闭包、多个独立监听、Once、取消监听及请求的异步返回值均通过同一客户端正确工作。
test("event callbacks stay local, once/unsubscribe and async request responses work",async()=>{
 const bus=fakeBus(),events=createEvents(bus.service,call),errors=[];events.onError(error=>errors.push(error));
 const received=[];const closure=40;
 try{
  const off=await events.on("demo:event",(data,meta)=>received.push(data.value+closure+meta.sender.length));
  await events.emit("demo:event",{value:2});await eventually(()=>received.length===1);assert.equal(received[0],53);
  await off();await events.emit("demo:event",{value:2});assert.equal(received.length,1);
  let once=0;await events.once("demo:once",()=>once++);await events.emit("demo:once",null);await events.emit("demo:once",null);await eventually(()=>once===1);
  await eventually(()=>bus.listeners.size===0);
  const stop=await events.handle("demo:request",async data=>({sum:data.value+closure}));
  assert.deepEqual(await events.request("demo:request",{value:2}),{sum:42});await stop();
  assert.ok(bus.calls.every(entry=>typeof entry.kind==="string"&&!Object.values(entry).some(value=>typeof value==="function")));
  await assert.rejects(events.emit("demo:event",{callback(){}}),{code:"E_INVALID_ARGUMENT"});
  assert.equal(errors.length,0);
 }finally{await events.close();}
});

// 验证 async 回调拒绝及不可序列化返回值会返回请求错误，而不是让请求悄悄挂起直到超时。
test("request handler failures and non-JSON results return errors",async()=>{
 const bus=fakeBus(),events=createEvents(bus.service,call);events.onError(()=>{});
 try{
  const off=await events.handle("demo:failure",async()=>{throw new Error("business failure");});
  await assert.rejects(events.request("demo:failure",null),{code:"E_HANDLER_ERROR"});await off();
  await events.handle("demo:function",()=>({callback(){}}));
  await assert.rejects(events.request("demo:function",null),{code:"E_HANDLER_ERROR"});
 }finally{await events.close();}
});

// 验证 Shell.run 可流式处理跨 UTF-8 字符边界的 Base64 输出，并在正常结束后释放历史记录。
test("shell callbacks decode split UTF-8 output and cleanup completed jobs",async()=>{
 const encoded=new TextEncoder().encode("中文");let readCount=0,removed=false;const chunks=[];
 const service={
  /** 返回固定任务编号，测试只覆盖 SDK 输出回调，不启动操作系统进程。 */
  async Start(){return{id:"job-1"};},
  /** 把中文 UTF-8 字节拆在字符内部，验证 SDK 不会按块丢失或替换半个字符。 */
  async Read(){readCount++;const done=readCount>1;return{job:{id:"job-1",state:done?"completed":"running",exitCode:done?0:-1},stdout:Buffer.from(done?encoded.subarray(1):encoded.subarray(0,1)).toString("base64"),stderr:"",stdoutOffset:done?encoded.length:1,stderrOffset:0};},
  /** 记录正常终态后的自动清理。 */
  async Remove(){removed=true;},
  /** 正常完成不应触发取消，否则测试立即失败。 */
  async Cancel(){throw new Error("completed job should not be cancelled");},
 };
 const shell=createShell(service,call);const result=await shell.run({program:"demo"},{onStdout:text=>chunks.push(text)});
 assert.equal(result.stdout,"中文");assert.equal(chunks.join(""),"中文");assert.equal(result.exitCode,0);assert.equal(removed,true);
});

// 模拟最后两个监听并发撤销，其中一个会话关闭先于另一个取消请求返回，迟到的取消应为幂等成功。
test("concurrent unsubscribe tolerates a session already closed by the last listener",async()=>{
 const bus=fakeBus(),events=createEvents(bus.service,call);events.onError(()=>{});
 const original=bus.service.Unsubscribe;let pending=0;
 bus.service.Unsubscribe=async(session,id)=>{
  pending++;if(pending===2){await new Promise(resolve=>setTimeout(resolve,5));if(!bus.sessions.has(session))throw Object.assign(new Error("expired"),{code:"E_SESSION_EXPIRED"});}
  return original(session,id);
 };
 try{const first=await events.on("demo:first",()=>{});const second=await events.on("demo:second",()=>{});await Promise.all([first(),second()]);assert.equal(bus.sessions.size,0);}
 finally{await events.close();}
});

// 环境检测在 SSR 下不得访问 window，在浏览器中只有宿主握手成功后才能标记为 desktop-shell。
test("environment detection distinguishes SSR, browser and verified shell",async()=>{
 const previousWindow=globalThis.window,previousDocument=globalThis.document;
 const client=createClient({async GetEnvironment(){return{kind:"desktop-shell",isDesktopShell:true,protocolVersion:1};}});
 try{
  delete globalThis.window;delete globalThis.document;assert.equal(client.isDesktopShell(),false);assert.equal((await client.detectEnvironment()).kind,"server");
  globalThis.window={};globalThis.document={};assert.equal(client.isDesktopShell(),false);
  const browser=createClient({async GetEnvironment(){throw new Error("offline");}});assert.equal((await browser.detectEnvironment()).kind,"browser");
  globalThis.window={chrome:{webview:{postMessage(){}}}};assert.equal(client.isDesktopShell(),true);assert.equal((await client.detectEnvironment()).isDesktopShell,true);
  const attached=createClient({async GetEnvironment(){return{kind:"browser-connected",isDesktopShell:false,protocolVersion:1};}});assert.equal((await attached.detectEnvironment()).isDesktopShell,false);
 }finally{if(previousWindow===undefined)delete globalThis.window;else globalThis.window=previousWindow;if(previousDocument===undefined)delete globalThis.document;else globalThis.document=previousDocument;}
});
