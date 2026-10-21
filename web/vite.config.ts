import react from '@vitejs/plugin-react';
import { execFileSync } from 'node:child_process';
import { defineConfig, type ProxyOptions } from 'vite';
import { readFileSync, watchFile, unwatchFile } from 'node:fs';
import { Agent, Server } from 'node:https';
import { X509Certificate } from 'node:crypto';

// Keep preview port overrides aligned with the managed backend.
const apiOrigin = `https://127.0.0.1:${process.env.PREVIEW_API_PORT || 19900}`;
// Media URLs are derived from Host, which must match the browser for scoped CORS.

// Container builds supply the source revision as a file; local builds read Git.
// Embed it in the assets so an older open tab keeps reporting its own version.
function clientVersion() {
  try {
    const cycle = readFileSync(new URL('../release-date.txt', import.meta.url), 'utf8').trim();
    let revision: string;
    try {
      revision = readFileSync(new URL('../web-build-revision', import.meta.url), 'utf8').trim();
    } catch {
      revision = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
    }
    return /^[0-9a-f]{40}$/.test(revision) ? `${cycle}_${revision.slice(0, 7)}` : 'Unknown';
  } catch {
    return 'Unknown';
  }
}

// https://vite.dev/config/
export default defineConfig(({ command }) => {
  // Build does not depend on local secrets. Development uses the exact server
  // leaf and verifies its backend against this instance's CA, never secure:false.
  const ca = command === 'serve' ? readFileSync('../server/data/tls/ca.pem') : undefined;
  const bundle = command === 'serve' ? readFileSync('../server/data/tls/server-bundle.pem') : undefined;
  // Validate a concrete SAN even when a public upload omits localhost. The
  // TCP destination stays loopback; CA/signature/name verification stays on.
  const certificateName = (data: Buffer) => {
    const certificate = new X509Certificate(data);
    const names = (certificate.subjectAltName || '').split(', ');
    return names.find((name) => name.startsWith('DNS:') && !name.includes('*'))?.slice(4) || names.find((name) => name.startsWith('IP Address:'))?.slice(11);
  };
  const proxyAgent = (trust: Buffer | undefined, cert: Buffer | undefined) =>
    new Agent({
      ca: trust,
      servername: cert ? certificateName(cert) : undefined,
    });
  const updateProxies: ((agent: Agent) => void)[] = [];
  const managementProxy: ProxyOptions = {
    target: apiOrigin,
    changeOrigin: false,
    agent: proxyAgent(ca, bundle),
    configure(_proxy, options) {
      updateProxies.push((agent) => {
        options.agent = agent;
      });
    },
  };
  return {
    define: { __APP_VERSION__: JSON.stringify(clientVersion()) },
    plugins: [
      react(),
      {
        name: 'instance-tls-reload',
        configureServer(server) {
          // A single atomic bundle prevents mismatched leaf/key reads during renewal.
          const path = '../server/data/tls/server-bundle.pem';
          const reload = () => {
            try {
              const updated = readFileSync(path);
              const agent = proxyAgent(readFileSync('../server/data/tls/ca.pem'), updated);
              // Existing sockets retain the old certificate. New proxy sockets
              // must verify the new CA/name rather than reuse the old pool.
              const previous = managementProxy.agent as Agent;
              managementProxy.agent = agent;
              updateProxies.forEach((update) => update(agent));
              previous.destroy();
              if (server.httpServer instanceof Server)
                server.httpServer.setSecureContext({
                  cert: updated,
                  key: updated,
                });
            } catch {
              server.config.logger.error('TLS reload failed; retaining the previous preview certificate.');
            }
          };
          watchFile(path, { interval: 500 }, reload);
          server.httpServer?.once('close', () => unwatchFile(path, reload));
        },
      },
    ],
    server: {
      https:
        command === 'serve'
          ? {
              cert: bundle,
              key: bundle,
            }
          : undefined,
      // Proxy API calls to the Go server's management listener during local
      // dev. Keep this in sync with server/internal/config default addresses.
      proxy: {
        '/healthz': managementProxy,
        '/api': managementProxy,
        '/media': managementProxy,
        '/profile-icons': managementProxy,
      },
    },
  };
});
