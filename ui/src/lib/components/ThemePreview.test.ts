import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import ThemePreview from './ThemePreview.svelte';

describe('ThemePreview', () => {
	it('sets the theme’s variables through the CSSOM and shows the logo', () => {
		const { container } = render(ThemePreview, {
			theme: { version: 1, base: 'dark', colors: { accent: '#7c3aed' }, radius: 5 },
			images: { logo: '/api/branding/assets/logo-0123456789abcdef.svg' },
			network: 'Example Chat'
		});
		const root = container.querySelector('.preview') as HTMLElement;
		expect(root.style.getPropertyValue('--accent')).toBe('#7c3aed');
		expect(root.style.getPropertyValue('--radius-md')).toBe('10px');
		expect(root.getAttribute('style')).not.toMatch(/javascript/);
		expect(screen.getByRole('img', { name: /boson app/i })).toBeTruthy();
		expect(container.querySelector('.tile.active img')?.getAttribute('src')).toBe(
			'/api/branding/assets/logo-0123456789abcdef.svg'
		);
		expect(screen.getByText('Example Chat')).toBeTruthy();
	});

	it('draws the sign-in screen with the background and network name', () => {
		const { container } = render(ThemePreview, {
			theme: { version: 1, base: 'light' },
			images: { login_background: '/api/branding/assets/login_background-0123456789abcdef.webp' },
			network: 'Example Chat',
			view: 'login'
		});
		expect(screen.getByText('Sign in to Example Chat')).toBeTruthy();
		expect(container.querySelector('.login-bg')?.getAttribute('src')).toMatch(/login_background-/);
		expect((container.querySelector('.preview') as HTMLElement).style.getPropertyValue('--bg')).toBe(
			'#f4f4f0'
		);
	});
});
