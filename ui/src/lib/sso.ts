import type { ProviderKind } from './types';

/** Where a guide step needs one of the URLs the auth service hands out. */
export type UrlRef = 'callback_url' | 'acs_url' | 'sp_entity_id' | 'sp_metadata_url';

export interface GuideStep {
	text: string;
	/** Shows this provider URL (with a copy button) under the step. */
	url?: UrlRef;
}

export interface Preset {
	id: string;
	label: string;
	kind: ProviderKind;
	blurb: string;
	slug: string;
	displayName: string;
	/** Placeholder for the issuer / metadata URL field. */
	placeholder: string;
	issuer?: string;
	scopes?: string;
	attributes?: { email: string; username: string; display_name: string };
	guide: GuideStep[];
}

export const presets: Preset[] = [
	{
		id: 'google',
		label: 'Google',
		kind: 'oidc',
		blurb: 'Google accounts and Workspace',
		slug: 'google',
		displayName: 'Google',
		issuer: 'https://accounts.google.com',
		placeholder: 'https://accounts.google.com',
		scopes: 'openid email profile',
		guide: [
			{
				text: 'In the Google Cloud console open APIs & Services → OAuth consent screen and set it up (External for any Google account, Internal for your Workspace only).'
			},
			{ text: 'Go to Credentials → Create credentials → OAuth client ID, application type Web application.' },
			{ text: 'Add this as an authorised redirect URI:', url: 'callback_url' },
			{ text: 'Copy the client ID and client secret into this form.' }
		]
	},
	{
		id: 'authentik',
		label: 'Authentik',
		kind: 'oidc',
		blurb: 'Self-hosted, OAuth2/OpenID provider',
		slug: 'authentik',
		displayName: 'Authentik',
		placeholder: 'https://auth.example.com/application/o/boson/',
		scopes: 'openid email profile',
		guide: [
			{
				text: 'In the admin interface open Applications → Providers → Create, and pick OAuth2/OpenID Provider.'
			},
			{ text: 'Client type Confidential. Add this redirect URI (strict):', url: 'callback_url' },
			{
				text: 'Create an Application that uses the provider. Its slug is part of the issuer: https://<authentik>/application/o/<slug>/.'
			},
			{ text: 'Copy the client ID and secret from the provider into this form.' }
		]
	},
	{
		id: 'zitadel',
		label: 'Zitadel',
		kind: 'oidc',
		blurb: 'Zitadel Cloud or self-hosted',
		slug: 'zitadel',
		displayName: 'Zitadel',
		placeholder: 'https://example-abc123.zitadel.cloud',
		scopes: 'openid email profile',
		guide: [
			{ text: 'In your project choose New → Application → Web, authentication method Code.' },
			{ text: 'Add this redirect URI:', url: 'callback_url' },
			{
				text: 'Copy the client ID and secret. The issuer is your instance domain, e.g. https://example-abc123.zitadel.cloud.'
			}
		]
	},
	{
		id: 'keycloak',
		label: 'Keycloak',
		kind: 'oidc',
		blurb: 'A realm on your Keycloak server',
		slug: 'keycloak',
		displayName: 'Keycloak',
		placeholder: 'https://sso.example.com/realms/boson',
		scopes: 'openid email profile',
		guide: [
			{ text: 'In your realm open Clients → Create client, type OpenID Connect.' },
			{
				text: 'Turn on Client authentication and keep Standard flow on. Add this as a valid redirect URI:',
				url: 'callback_url'
			},
			{ text: 'Copy the secret from the Credentials tab. The issuer is https://<keycloak>/realms/<realm>.' }
		]
	},
	{
		id: 'oidc',
		label: 'Other OpenID Connect',
		kind: 'oidc',
		blurb: 'Any provider with OIDC discovery',
		slug: 'sso',
		displayName: 'Company sign-in',
		placeholder: 'https://idp.example.com',
		scopes: 'openid email profile',
		guide: [
			{ text: 'Register a confidential web client (authorisation code flow) with your provider.' },
			{ text: 'Use this redirect URI:', url: 'callback_url' },
			{
				text: 'The issuer must serve /.well-known/openid-configuration. Copy the client ID and secret into this form.'
			}
		]
	},
	{
		id: 'okta',
		label: 'Okta',
		kind: 'saml',
		blurb: 'SAML app integration',
		slug: 'okta',
		displayName: 'Okta',
		placeholder: 'https://example.okta.com/app/…/sso/saml/metadata',
		attributes: { email: 'email', username: 'username', display_name: 'name' },
		guide: [
			{ text: 'In the Okta admin console open Applications → Create App Integration → SAML 2.0.' },
			{ text: 'Single sign-on URL:', url: 'acs_url' },
			{ text: 'Audience URI (SP entity ID):', url: 'sp_entity_id' },
			{
				text: 'Add attribute statements email → user.email, username → user.login, name → user.displayName.'
			},
			{
				text: 'On the Sign On tab copy the Metadata URL into this form, then assign the app to people or groups.'
			}
		]
	},
	{
		id: 'entra',
		label: 'Microsoft Entra ID',
		kind: 'saml',
		blurb: 'Azure AD enterprise application',
		slug: 'entra',
		displayName: 'Microsoft',
		placeholder: 'https://login.microsoftonline.com/<tenant>/federationmetadata/…',
		attributes: {
			email: 'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress',
			username: 'http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name',
			display_name: 'http://schemas.microsoft.com/identity/claims/displayname'
		},
		guide: [
			{
				text: 'In Entra admin center open Enterprise applications → New application → Create your own (non-gallery).'
			},
			{
				text: 'Single sign-on → SAML → Basic SAML Configuration. Identifier (Entity ID):',
				url: 'sp_entity_id'
			},
			{ text: 'Reply URL (Assertion Consumer Service URL):', url: 'acs_url' },
			{
				text: 'Under SAML Certificates copy the App Federation Metadata Url into this form, then assign users and groups.'
			}
		]
	},
	{
		id: 'saml',
		label: 'Other SAML',
		kind: 'saml',
		blurb: 'Any SAML 2.0 identity provider',
		slug: 'saml',
		displayName: 'Company sign-in',
		placeholder: 'https://idp.example.com/metadata',
		attributes: { email: 'email', username: 'username', display_name: 'displayName' },
		guide: [
			{
				text: 'Create a SAML 2.0 service provider (app) in your IdP. Assertion Consumer Service URL:',
				url: 'acs_url'
			},
			{ text: 'SP entity ID (audience):', url: 'sp_entity_id' },
			{ text: 'Or import boson’s SP metadata directly:', url: 'sp_metadata_url' },
			{
				text: 'Release email, username and display name attributes, and paste the IdP metadata URL or XML here.'
			}
		]
	}
];

export const urlLabels: Record<UrlRef, string> = {
	callback_url: 'Redirect (callback) URL',
	acs_url: 'ACS URL',
	sp_entity_id: 'SP entity ID',
	sp_metadata_url: 'SP metadata URL'
};

/** A slug from a display name: lowercase, dashes, 2–40 chars. */
export function slugify(name: string): string {
	return name
		.toLowerCase()
		.normalize('NFKD')
		.replace(/[̀-ͯ]/g, '')
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-+|-+$/g, '')
		.slice(0, 40)
		.replace(/-+$/, '');
}

/** Picks the preset that best matches a saved provider (for its guide). */
export function presetFor(kind: ProviderKind, issuerOrMetadata: string, slug: string): Preset {
	const s = (issuerOrMetadata + ' ' + slug).toLowerCase();
	const hit = (id: string) => presets.find((p) => p.id === id)!;
	if (kind === 'oidc') {
		if (s.includes('accounts.google.com')) return hit('google');
		if (s.includes('/application/o/') || s.includes('authentik')) return hit('authentik');
		if (s.includes('zitadel')) return hit('zitadel');
		if (s.includes('/realms/') || s.includes('keycloak')) return hit('keycloak');
		return hit('oidc');
	}
	if (s.includes('okta')) return hit('okta');
	if (s.includes('microsoftonline') || s.includes('entra') || s.includes('azure')) return hit('entra');
	return hit('saml');
}
