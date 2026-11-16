import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vitest/config';

// The relay embeds build/app (see embed.go) and serves it as a SPA: its
// index.html is the fallback page for "/" and "/s/<id>".
const relayTarget = process.env.HERDR_SHARE_DEV_RELAY ?? 'http://localhost:8080';

export default defineConfig({
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},
			adapter: adapter({
				pages: 'build/app',
				assets: 'build/app',
				fallback: 'index.html',
				strict: true
			})
		})
	],
	server: {
		proxy: {
			'/api': { target: relayTarget, ws: true },
			'/healthz': relayTarget
		}
	},
	test: {
		include: ['src/**/*.test.ts'],
		environment: 'node'
	}
});
