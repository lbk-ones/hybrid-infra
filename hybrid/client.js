// This facade is copied into hybrid/client.js by scripts/generate-hybrid.mjs.
// The generated bindings below keep their original paths and API.
import * as Runtime from './runtime.js';
export { Runtime };
export * as UI from './bindings/hybrid-infra/go/services/uiservice.js';
export * as Store from './bindings/github.com/wailsapp/wails/v3/pkg/services/kvstore/kvstoreservice.js';
export * as Log from './bindings/github.com/wailsapp/wails/v3/pkg/services/log/logservice.js';

// Call before your first service request. No framework or build tool is required.
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
