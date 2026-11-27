import { describe, expect, it } from 'vitest';
import { endpointHost, reasonText, resultText, resultTone } from './telemetry';

describe('telemetry result text', () => {
	it('says what happened, never more than a class', () => {
		expect(resultText(undefined)).toBe('Nothing sent yet');
		expect(resultText({ at: 'x', reason: 'daily', ok: true, status: 202 })).toBe('Received');
		expect(resultText({ at: 'x', reason: 'daily', ok: false, status: 500 })).toBe('Refused (HTTP 500)');
		expect(resultText({ at: 'x', reason: 'daily', ok: false, error: 'refused' })).toBe(
			'Not sent: the connection was refused'
		);
		expect(resultText({ at: 'x', reason: 'daily', ok: false, error: 'mystery' })).toBe(
			'Not sent: a network error'
		);
		expect(resultTone(undefined)).toBe('none');
		expect(resultTone({ at: 'x', reason: 'daily', ok: false })).toBe('bad');
	});
	it('names reasons and the endpoint host', () => {
		expect(reasonText('setup')).toBe('the first report of this install');
		expect(reasonText(undefined)).toBe('the daily report');
		expect(endpointHost('https://telemetry.boson.chat/v1/report')).toBe('telemetry.boson.chat');
		expect(endpointHost('not a url')).toBe('telemetry.boson.chat');
	});
});
