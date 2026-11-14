/// <reference lib="webworker" />
// Compiles MJML off the main thread. mjml-browser (pinned in package.json)
// is a 1.2 MB UMD bundle: it's loaded as a plain script from the panel's
// own origin on the first compile (importScripts), so it runs as written,
// non-strict, and never needs eval (the CSP has no 'unsafe-eval').
import mjmlUrl from 'mjml-browser/lib/index.js?url';
import { compile, type MjmlFn } from './compile';
import type { CompileReply, CompileRequest } from './compiler';

const scope = self as unknown as DedicatedWorkerGlobalScope & { mjml?: MjmlFn };
let mjml: MjmlFn | undefined;

function load(): MjmlFn {
	if (!mjml) {
		scope.importScripts(new URL(mjmlUrl, scope.location.href).href);
		if (typeof scope.mjml !== 'function') throw new Error('mjml-browser didn’t load');
		mjml = scope.mjml;
	}
	return mjml;
}

scope.onmessage = async (e: MessageEvent<CompileRequest>) => {
	const { id, source, part } = e.data;
	let reply: CompileReply;
	try {
		reply = { id, result: await compile(load(), source, part) };
	} catch (err) {
		reply = { id, error: err instanceof Error ? err.message : String(err) };
	}
	scope.postMessage(reply);
};
