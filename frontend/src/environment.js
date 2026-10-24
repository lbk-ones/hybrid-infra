/** 同步检查原生桥接特征，只用于选择界面体验；对象可被页面伪造，实际能力仍以宿主权限校验为准。 */
export function isDesktopShell() {
  if (typeof window === "undefined" || typeof document === "undefined") return false;
  return Boolean(window._wails?.environment?.OS || window.chrome?.webview?.postMessage || window.webkit?.messageHandlers?.external?.postMessage || window.external?.invoke || window.wails?.invoke);
}

/** 调用宿主握手确认环境；SSR、普通浏览器、连接到宿主的浏览器和真正的原生窗口分别返回明确类型。 */
export async function detectEnvironment(probe) {
  if (typeof window === "undefined" || typeof document === "undefined") return { kind: "server", isDesktopShell: false, connected: false, bridgeDetected: false };
  const bridgeDetected = isDesktopShell();
  try {
    const result = await probe();
    if (result?.protocolVersion !== 1 || !["desktop-shell", "browser-connected"].includes(result.kind) || result.isDesktopShell !== (result.kind === "desktop-shell")) throw Object.assign(new Error("宿主环境响应不匹配"), { code: "E_PROTOCOL_MISMATCH" });
    return { ...result, connected: true, bridgeDetected };
  } catch (error) {
    return { kind: bridgeDetected ? "desktop-unavailable" : "browser", isDesktopShell: false, connected: false, bridgeDetected, errorCode: error?.code || "E_TRANSPORT" };
  }
}
