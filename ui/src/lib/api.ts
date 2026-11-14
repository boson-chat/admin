import { goto } from '$app/navigation';
import { session } from './session.svelte';

/** The upstream doesn't have this feature yet (an older auth or API release). */
export function isUnsupported(e: unknown): e is ApiError {
	return e instanceof ApiError && e.code === 'upstream_unsupported';
}

export class ApiError extends Error {
	constructor(
		public status: number,
		public code: string,
		message: string
	) {
		super(message);
	}
}

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';

/**
 * Calls the panel's BFF. The session lives in an HttpOnly cookie; state-
 * changing calls echo the CSRF token the BFF handed out with the session.
 */
export async function api<T>(
	method: Method,
	path: string,
	body?: unknown,
	extraHeaders?: Record<string, string>
): Promise<T> {
	const headers: Record<string, string> = { Accept: 'application/json', ...extraHeaders };
	if (body !== undefined) headers['Content-Type'] = 'application/json';
	if (method !== 'GET') {
		headers['X-Requested-With'] = 'boson-admin';
		if (session.info) headers['X-CSRF-Token'] = session.info.csrf_token;
	}
	let res: Response;
	try {
		res = await fetch(`/api${path}`, {
			method,
			headers,
			body: body === undefined ? undefined : JSON.stringify(body),
			credentials: 'same-origin'
		});
	} catch {
		throw new ApiError(0, 'network', 'The admin panel server is unreachable. Check your connection.');
	}
	if (res.status === 204) return undefined as T;
	let data: unknown = null;
	try {
		data = await res.json();
	} catch {
		/* non-JSON error page from a proxy */
	}
	if (!res.ok) {
		const e = (data ?? {}) as { error?: string; message?: string };
		const err = new ApiError(res.status, e.error ?? 'http_' + res.status, e.message ?? res.statusText);
		if (res.status === 401 && path !== '/login' && path !== '/session') {
			session.clear(err.message);
			goto('/login?next=' + encodeURIComponent(location.pathname + location.search));
		}
		throw err;
	}
	return data as T;
}

/**
 * Sends a file as the raw request body (an image upload). Same CSRF and
 * error handling as api().
 */
export async function upload<T>(
	method: 'PUT' | 'POST',
	path: string,
	file: Blob,
	extraHeaders?: Record<string, string>
): Promise<T> {
	const headers: Record<string, string> = {
		Accept: 'application/json',
		'Content-Type': file.type && file.type.startsWith('image/') ? file.type : 'application/octet-stream',
		'X-Requested-With': 'boson-admin',
		...extraHeaders
	};
	if (session.info) headers['X-CSRF-Token'] = session.info.csrf_token;
	let res: Response;
	try {
		res = await fetch(`/api${path}`, { method, headers, body: file, credentials: 'same-origin' });
	} catch {
		throw new ApiError(0, 'network', 'The admin panel server is unreachable. Check your connection.');
	}
	let data: unknown = null;
	try {
		data = await res.json();
	} catch {
		/* non-JSON error page from a proxy */
	}
	if (!res.ok) {
		const e = (data ?? {}) as { error?: string; message?: string };
		if (res.status === 413 && !e.message) e.message = 'The image is too large (2 MB at most).';
		throw new ApiError(res.status, e.error ?? 'http_' + res.status, e.message ?? res.statusText);
	}
	return data as T;
}
