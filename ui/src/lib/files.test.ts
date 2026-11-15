import { describe, expect, it } from 'vitest';
import {
	changes,
	draftFrom,
	formatDuration,
	formatSize,
	humanDuration,
	humanSize,
	parseDuration,
	parseSize,
	usageLine,
	validate,
	DEFAULTS,
	type Draft,
	type FilesStatus
} from './files';
import type { ConfigView } from './types';

const view = (keys: Record<string, unknown>): ConfigView => ({
	enabled: true,
	exists: true,
	issues: [],
	keys: Object.entries(keys).map(([key, value]) => ({
		key,
		kind: 'string',
		perm: 'settings.manage',
		services: ['files'],
		set: true,
		value: value as never,
		editable: true
	}))
});

describe('sizes', () => {
	it('parses human units (binary)', () => {
		expect(parseSize('5GB')).toBe(5 * 2 ** 30);
		expect(parseSize('500 mb')).toBe(500 * 2 ** 20);
		expect(parseSize('1.5GB')).toBe(1536 * 2 ** 20);
		expect(parseSize('25MiB')).toBe(25 * 2 ** 20);
		expect(parseSize('lots')).toBeNull();
	});
	it('formats in the file’s form and for people', () => {
		expect(formatSize(5 * 2 ** 30)).toBe('5GB');
		expect(formatSize(1536 * 2 ** 20)).toBe('1536MB');
		expect(humanSize(1.2 * 2 ** 30)).toBe('1.2 GB');
		expect(humanSize(900)).toBe('900 B');
	});
});

describe('durations', () => {
	it('reads 72h, 3d, 3 days, 90m, 1h30m', () => {
		expect(parseDuration('72h')).toBe(259200);
		expect(parseDuration('3d')).toBe(259200);
		expect(parseDuration('3 days')).toBe(259200);
		expect(parseDuration('90m')).toBe(5400);
		expect(parseDuration('1h30m')).toBe(5400);
		expect(parseDuration('soon')).toBeNull();
	});
	it('writes Go durations and says them in words', () => {
		expect(formatDuration(259200)).toBe('72h');
		expect(formatDuration(5400)).toBe('90m');
		expect(humanDuration(259200)).toBe('3 days');
		expect(humanDuration(3600)).toBe('1 hour');
	});
});

describe('the form', () => {
	const status: FilesStatus = {
		storage: { driver: 'local', ok: true },
		totals: { files: 0, bytes: 0, window_uploads: 0, window_bytes: 0, uploaders: 0 },
		config: {
			enabled: true,
			guest_enabled: false,
			retention_seconds: 86400,
			limits: {
				account: { quota_bytes: 10 * 2 ** 30, window_seconds: 259200, max_file_bytes: 200 * 2 ** 20 },
				guest: { quota_bytes: 500 * 2 ** 20, window_seconds: 259200, max_file_bytes: 25 * 2 ** 20 }
			}
		},
		errors: []
	};
	it('takes the file, then the service, then the defaults', () => {
		const { draft, sources } = draftFrom(view({ 'files.account.quota': '7GB' }), status);
		expect(draft.account_quota).toBe('7GB');
		expect(sources.account_quota).toBe('file');
		expect(draft.retention).toBe('24h');
		expect(sources.retention).toBe('service');
		expect(draft.guest_enabled).toBe(false);
		expect(draft.s3_region).toBe('auto');
		expect(sources.s3_region).toBe('default');
		const plain = draftFrom(null, null).draft;
		expect(plain).toEqual(DEFAULTS);
	});
	it('saves normalised values and unsets cleared fields', () => {
		const saved: Draft = { ...DEFAULTS };
		const d: Draft = { ...saved, enabled: true, retention: '3d', account_quota: '10 gb', s3_region: '' };
		expect(changes(d, saved)).toEqual({
			set: { 'files.enabled': true, 'files.account.quota': '10GB' },
			unset: ['files.storage.s3.region']
		});
		// 3d is 72h: no change.
		expect(changes({ ...saved, retention: '3 days' }, saved)).toEqual({});
	});
	it('validates', () => {
		const ok: Draft = { ...DEFAULTS };
		expect(validate(ok)).toEqual({});
		expect(validate({ ...ok, account_quota: 'lots' }).account_quota).toMatch(/size/);
		expect(validate({ ...ok, guest_max_file: '1GB' }).guest_max_file).toMatch(/bigger than the quota/);
		expect(validate({ ...ok, retention: '10s' }).retention).toMatch(/1 minute/);
		const s3 = validate({ ...ok, driver: 's3' });
		expect(s3.s3_endpoint).toMatch(/Needed/);
		expect(s3.s3_bucket).toMatch(/Needed/);
		expect(validate({ ...ok, s3_public_base_url: 'http://cdn.example.com' }).s3_public_base_url).toBeTruthy();
		expect(validate({ ...ok, s3_endpoint: 'http://minio:9000' }).s3_endpoint).toBeUndefined();
	});
	it('describes usage', () => {
		const now = Date.parse('2026-11-14T00:00:00Z');
		expect(
			usageLine(
				{
					used: 1.2 * 2 ** 30,
					quota: 5 * 2 ** 30,
					window_seconds: 259200,
					max_file: 100 * 2 ** 20,
					frees: [{ bytes: 2 ** 30, at: '2026-11-14T20:00:00Z' }]
				},
				now
			)
		).toBe('1.2 GB of 5 GB, 1 GB frees in 20 h');
	});
});
