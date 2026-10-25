import { defineConfig } from "vite";
import wails from "@wailsio/runtime/plugins/vite";
import { fileURLToPath } from "node:url";

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [wails("./bindings")],
  build: {
    rollupOptions: {
      input: {
        splash: fileURLToPath(new URL("./shell/splash.html", import.meta.url)),
        maintenance: fileURLToPath(new URL("./shell/maintenance.html", import.meta.url)),
        main: fileURLToPath(new URL("./index.html", import.meta.url)),
        status: fileURLToPath(new URL("./shell/status.html", import.meta.url)),
        manage: fileURLToPath(new URL("./shell/manage.html", import.meta.url)),
        desktop: fileURLToPath(new URL("./shell/desktop.html", import.meta.url)),
        bridge: fileURLToPath(new URL("./shell/bridge.html", import.meta.url)),
        ui: fileURLToPath(new URL("./shell/ui.html", import.meta.url)),
        capture: fileURLToPath(new URL("./shell/capture.html", import.meta.url)),
      },
    },
  },
});
