import assert from "node:assert/strict";
import test from "node:test";
import { createUI } from "../frontend/src/ui-client.js";

// 验证旧四参数调用不改变 RPC 参数数量，新对象和第五参数写法能准确保留显式关闭拖拽。
test("UI facade preserves old Alert and forwards draggable options",()=>{
 const calls=[];
 const bindings={
  /** 记录旧版四参数调用，便于检查兼容性。 */
  Alert(...args){calls.push({method:"Alert",args});return "legacy";},
  /** 记录对象参数，模拟自动生成的原生绑定。 */
  AlertWithOptions(req){calls.push({method:"AlertWithOptions",req});return "options";},
  /** 记录通知参数，默认值交给 Go 填充。 */
  Notify(req){calls.push({method:"Notify",req});return "notification";},
 };
 const UI=createUI(bindings);
 assert.equal(UI.Alert("title","body",2,"topRight"),"legacy");assert.deepEqual(calls.at(-1).args,["title","body",2,"topRight"]);
 UI.Alert("title","body",2,"topRight",{draggable:false});assert.equal(calls.at(-1).req.draggable,false);
 UI.Alert({title:"object",draggable:false});assert.deepEqual(calls.at(-1).req,{title:"object",draggable:false});
 UI.Notify("完成");assert.deepEqual(calls.at(-1).req,{text:"完成"});
 UI.Notify("完成",{draggable:false,durationSeconds:8,place:"topLeft"});assert.deepEqual(calls.at(-1).req,{text:"完成",draggable:false,durationSeconds:8,place:"topLeft"});
});
