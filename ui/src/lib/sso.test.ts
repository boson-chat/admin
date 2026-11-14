import { describe, expect, it } from 'vitest';
import { presetFor, slugify } from './sso';

describe('slugify', () => {
	it('makes URL-safe slugs', () => {
		expect(slugify('Acme Okta')).toBe('acme-okta');
		expect(slugify('  Zürich SSO!! ')).toBe('zurich-sso');
		expect(slugify('x'.repeat(50) + ' y')).toHaveLength(40);
	});
});

describe('presetFor', () => {
	it('recognises common providers', () => {
		expect(presetFor('oidc', 'https://accounts.google.com', 'google').id).toBe('google');
		expect(presetFor('oidc', 'https://sso.example.com/realms/boson', 'kc').id).toBe('keycloak');
		expect(presetFor('oidc', 'https://a.example.com/application/o/boson/', 'x').id).toBe('authentik');
		expect(presetFor('saml', 'https://acme.okta.com/app/x/sso/saml/metadata', 'acme').id).toBe('okta');
		expect(presetFor('saml', 'https://login.microsoftonline.com/t/federationmetadata', 'ms').id).toBe(
			'entra'
		);
		expect(presetFor('oidc', 'https://idp.example.com', 'idp').id).toBe('oidc');
	});
});
