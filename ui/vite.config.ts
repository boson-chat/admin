import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

// `npm run dev` proxies /api to a locally running BFF (default :8090, see
// docker-compose.yml). The Origin header is rewritten to the BFF's
// PUBLIC_URL so its same-origin checks pass.
const bff =
	(globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env.BFF_URL ??
	'http://localhost:8090';

export default defineConfig({
	plugins: [sveltekit()],
	build: {
		// Never inline fonts as data: URIs; the BFF's CSP allows `font-src 'self'` only.
		assetsInlineLimit: (file) => (/\.(woff2?|ttf|otf)$/.test(file) ? false : undefined)
	},
	server: {
		proxy: {
			'/api': { target: bff, changeOrigin: true, headers: { origin: bff } }
		}
	}
});
