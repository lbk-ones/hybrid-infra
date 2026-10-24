// This facade is copied into hybrid/client.js by scripts/generate-hybrid.mjs.
// The generated bindings below keep their original paths and API.
import * as Runtime from './runtime.js';
export { Runtime };
import * as UIBindings from './bindings/hybrid-infra/go/services/uiservice.js';
import { createUI } from './ui-client.js';
export const UI = createUI(UIBindings);
export * as Store from './bindings/github.com/wailsapp/wails/v3/pkg/services/kvstore/kvstoreservice.js';
export * as Log from './bindings/github.com/wailsapp/wails/v3/pkg/services/log/logservice.js';
import * as Host from './bindings/hybrid-infra/go/services/hostservice.js';
import * as Windows from './bindings/hybrid-infra/go/services/windowservice.js';
import * as Desktop from './bindings/hybrid-infra/go/services/desktopservice.js';
import * as Cache from './bindings/hybrid-infra/go/services/cacheservice.js';
import * as EventService from './bindings/hybrid-infra/go/services/eventservice.js';
import * as ShellService from './bindings/hybrid-infra/go/services/shellservice.js';
import { createEvents } from './events-client.js';
import { createShell } from './shell-client.js';
import { createClient } from './sdk-core.js';
export { Host, Windows, Desktop, Cache };
export { HybridError, normalizeError } from './sdk-core.js';
export const Hybrid = createClient(Host);
export const Events = createEvents(EventService, Hybrid.call);
export const Shell = createShell(ShellService, Hybrid.call);
// 页面卸载尽力释放会话；来不及送达的关闭请求仍会由宿主的闲置过期策略清理。
if (typeof window !== 'undefined') window.addEventListener('pagehide', () => { void Events.close().catch(() => {}); });

// 独立前端可以直接导入此文件；首次服务调用前按需配置宿主来源，不依赖特定前端框架。
/**
 * 配置独立前端连接宿主时使用的来源地址和通信适配器，应在首次服务调用前设置。
 * origin 只允许协议与主机组成的地址；transport 需要自行处理调用、取消和窗口路由。
 */
export function configureHybrid({ origin, transport } = {}) {
 if (origin !== undefined) {
  const url = new URL(origin);
  if (!['http:', 'https:', 'wails:'].includes(url.protocol) || !url.host || url.username || url.password || url.search || url.hash || url.pathname && url.pathname !== '/') {
   throw new TypeError('origin must be scheme://host[:port]');
  }
  window.__HYBRID_RUNTIME_ORIGIN__ = origin.replace(/\/$/, '');
 }
 if (transport !== undefined) Runtime.setTransport(transport);
}
