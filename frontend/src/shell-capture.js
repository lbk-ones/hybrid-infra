import { Desktop, Hybrid, normalizeError } from "./host-client.js";
/** 读取当前截图页的固定控件。 */
const $ = (id) => document.getElementById(id);
let displays=[];
let allowed=false;
let clipboardAllowed=false;
let busy=false;
/** 根据授权及截图状态控制按钮，避免并发弹出多个遮罩。 */
function update(){for(const id of ["capture-region-button","capture-select-button"])$(id).disabled=!allowed||busy;}
/** 选择显示器时显示物理坐标和逻辑尺寸，同时为坐标输入提供明确起点。 */
function showDisplay(){const display=displays.find(item=>item.id===$("capture-display").value);if(!display)return;const r=display.physical;$("display-info").textContent=`物理 ${r.x},${r.y} / ${r.width}×${r.height} · 逻辑 ${display.logical.width}×${display.logical.height}`;$("capture-x").value=r.x;$("capture-y").value=r.y;$("capture-width").value=Math.min(r.width,400);$("capture-height").value=Math.min(r.height,240);}
/** 调用真实截图服务，手动模式给宿主两分钟交互时间；结果只显示在当前页面，不发送日志或外部服务。 */
async function capture(mode){
 if(busy)return;busy=true;update();$("capture-notice").textContent=mode==="select"?"请在选定显示器拖动框选，Escape 取消":"正在采集…";
 try{
  const output=$("capture-output").value;if(output!=="base64"&&!clipboardAllowed)throw new Error("E_PERMISSION_DENIED: 写入剪贴板还需 clipboard 权限");
  const req={mode,x:Number($("capture-x").value),y:Number($("capture-y").value),width:Number($("capture-width").value),height:Number($("capture-height").value),displayId:$("capture-display").value,output};
  const result=await Hybrid.call(()=>Desktop.Screenshot(req),{timeoutMs:mode==="select"?125000:30000});
  $("capture-base64").value=result.base64||"";$("capture-preview").hidden=!result.base64;
  if(result.base64)$("capture-preview").src=`data:${result.mime};base64,${result.base64}`;else $("capture-preview").removeAttribute("src");
  $("capture-result-size").textContent=`${result.region.width} × ${result.region.height}`;
  $("capture-result-info").textContent=`物理区域 ${result.region.x},${result.region.y} · ${result.clipboard?"已写入系统剪贴板":"未写入剪贴板"}`;
  $("capture-notice").textContent="截图完成";$("capture-notice").className="desktop-wide";
 }catch(error){const value=normalizeError(error);$("capture-notice").textContent=`${value.code} · ${value.message}`;$("capture-notice").className="notice error desktop-wide";}
 finally{busy=false;update();}
}
$("capture-display").addEventListener("change",showDisplay);
$("capture-region-button").addEventListener("click",()=>capture("region"));
$("capture-select-button").addEventListener("click",()=>capture("select"));
/** 建立来源授权后再列出显示器；普通浏览器预览不会伪造屏幕图像或剪贴板成功状态。 */
async function initialize(){const info=await Hybrid.connect({requiredCapabilities:["screenshot"]});clipboardAllowed=info.capabilities.includes("clipboard");displays=await Hybrid.call(()=>Desktop.CaptureDisplays());for(const display of displays){const option=document.createElement("option");option.value=display.id;option.textContent=`${display.name}${display.primary?" · 主屏":""}`;$("capture-display").append(option);}const primary=displays.find(item=>item.primary);if(primary)$("capture-display").value=primary.id;showDisplay();allowed=true;$("capture-capability").textContent="screenshot 已授权";$("capture-notice").textContent="宿主已连接，截图不会自动保存到磁盘";update();}
initialize().catch(error=>{const value=normalizeError(error);$("capture-notice").textContent=`${value.code} · ${value.message}`;$("capture-capability").textContent="截图未授权或未连接";});
