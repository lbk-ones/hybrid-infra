import { Call } from "/wails/runtime.js";
let readyID;
/** 内容和 runtime 均就绪后通知宿主开始计时；避免 WebView 首次加载时间占用用户的四秒展示时长。 */
function ready() {
 const data=window.__HYBRID_DIALOG_DATA__;
 if(!data?.notification||readyID===data.id)return;
 readyID=data.id;
 Call.ByName("hybrid-infra/go/services.UiService.NotificationReady",data.id).catch(()=>{readyID=undefined;});
}
/** 仅关闭当前通知，走 ui 服务而不是要求调用者额外拥有原生 windows 权限。 */
function closeNotification(){const id=window.__HYBRID_DIALOG_DATA__?.id;if(id)void Call.ByName("hybrid-infra/go/services.UiService.CloseNotification",id).catch(()=>{});}
window.addEventListener("hybrid:dialog-config",ready);
document.getElementById("notification-close").addEventListener("click",closeNotification);
document.addEventListener("keydown",(event)=>{if(event.key==="Escape")closeNotification();});
ready();
