import { Hybrid, UI, normalizeError } from "./host-client.js";
/** 按固定控件编号读取当前演示页面，不使用用户文本构造选择器或 HTML。 */
const $ = (id) => document.getElementById(id);
let connected = false;
let confirming = false;

/** 按真实握手状态启用入口，通知不使用全局忙碌锁，允许连续点击验证堆叠。 */
function updateButtons() { for (const button of document.querySelectorAll("button")) button.disabled = !connected || (button.id === "ui-confirm" && confirming); }
/** 记录纯文本结果并限制长度，不把通知文本当成可执行内容。 */
function record(text) { $("ui-output").value = `${new Date().toLocaleTimeString()} · ${text}\n${$("ui-output").value}`.slice(0,10000); }
/** 将宿主错误显示在固定状态区域，保持其他通知按钮可以继续使用。 */
function errorNotice(error) { const value=normalizeError(error); $("ui-notice").textContent=`${value.code} · ${value.message}`; $("ui-notice").className="notice error desktop-wide"; }
/** 读取统一通知参数；不传默认参数的示例会绕过此函数，验证服务端真实默认值。 */
function options() { return { text:$("notify-text").value,place:$("ui-place").value,durationSeconds:Number($("notify-duration").value),draggable:$("ui-draggable").checked }; }
/** 短操作按钮统一捕获错误，不阻止连续通知；每次调用均交由宿主分配独立窗口和生命周期。 */
function bind(id,operation) { $(id).addEventListener("click",()=>{Promise.resolve().then(operation).catch(errorNotice);}); }

bind("notify-one",async()=>{const id=await Hybrid.call(()=>UI.Notify(options()));record(`通知已发送 · ${id}`);});
bind("notify-stack",async()=>{
 const req=options();
 // 顺序等待创建 RPC，保证三条文本编号与屏幕从上向下的顺序一致。
 for(let i=1;i<=3;i++){await Hybrid.call(()=>UI.Notify({...req,text:`${i} / ${req.text}`}));}
 record("已连续发送三条通知");
});
bind("notify-default",async()=>{await Hybrid.call(()=>UI.Notify({text:$("notify-text").value}));record("仅传 text：右上角 / 四秒 / 允许拖拽");});
bind("ui-alert",async()=>{await Hybrid.call(()=>UI.Alert({title:"操作完成",body:"可用标题或空白区域拖拽，三秒后自动关闭。",destroy:3,place:$("ui-place").value,draggable:$("ui-draggable").checked}));record("消息框已展示");});
bind("ui-confirm",async()=>{
 if(confirming)return;confirming=true;updateButtons();
 try{const result=await Hybrid.call(()=>UI.Confirm({title:"确认操作",body:"此确认框使用当前拖拽开关。",place:$("ui-place").value,draggable:$("ui-draggable").checked}),{timeoutMs:300000});record(`确认框返回 ${result}`);}
 finally{confirming=false;updateButtons();}
});
bind("ui-show",async()=>{await Hybrid.call(()=>UI.Show({body:'<div class="sample-card"><span class="sample-avatar">H</span><div><strong>自定义浮窗</strong><p>默认可拖拽，按钮保留正常交互。</p></div><button class="sample-close" onclick="closeWindow()" aria-label="关闭浮窗">×</button></div>',width:360,height:120,place:$("ui-place").value,draggable:$("ui-draggable").checked}));record("自定义浮窗已展示");});

/** 验证真实 ui 授权后启用演示；普通浏览器不显示虚假的通知创建成功。 */
async function initialize(){await Hybrid.connect({requiredCapabilities:["ui"]});connected=true;$("ui-notice").textContent="宿主已连接 · 可重复点击发送通知";updateButtons();}
initialize().catch(errorNotice);
