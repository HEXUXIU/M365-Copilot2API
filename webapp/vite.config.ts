import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Build output lands directly in the Go embed directory so `go build` picks
// it up with the existing //go:embed all:web directive — no copy step.
export default defineConfig({
  plugins: [react()],
  base: "/webapp/",
  build: {
    // Output lands inside the //go:embed all:web tree (internal/web/web/) so
    // the binary picks it up directly — no copy step.
    outDir: "../internal/web/web/webapp",
    emptyOutDir: true,
    sourcemap: false,
  },
});
