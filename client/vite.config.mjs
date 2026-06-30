import { defineConfig } from 'vite';
import { extensions, classicEmberSupport, ember } from '@embroider/vite';
import { babel } from '@rollup/plugin-babel';

export default defineConfig({
  plugins: [
    classicEmberSupport(),
    ember(),
    // extra plugins here
    babel({
      babelHelpers: 'runtime',
      extensions,
    }),
  ],
  // Dev server is fronted by the global Caddy (see ../dist/Caddyfile), which
  // terminates TLS at https://ecv3.localhost:8443 and proxies non-/api traffic
  // here. Pin the port so the Caddy fragment is deterministic, and point the
  // HMR websocket back at the Caddy origin so hot reload works through the proxy.
  server: {
    port: 4201,
    strictPort: true,
    hmr: {
      host: 'ecv3.localhost',
      clientPort: 8443,
      protocol: 'wss',
    },
  },
});
