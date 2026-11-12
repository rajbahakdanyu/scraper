// Hides the header while scrolling down and shows it again on any scroll up.
(() => {
	const root = document.documentElement;
	let lastY = Math.max(scrollY, 0);

	const menuOpen = () => {
		try {
			return Boolean(document.querySelector('#starlight__sidebar:popover-open'));
		} catch {
			return false;
		}
	};

	const update = () => {
		const y = Math.max(scrollY, 0);
		const delta = y - lastY;
		if (Math.abs(delta) < 6) return;
		lastY = y;
		const header = document.querySelector('header.header');
		const keep = y < 80 || menuOpen() || header?.contains(document.activeElement);
		root.toggleAttribute('data-header-hidden', delta > 0 && !keep);
	};

	addEventListener('scroll', update, { passive: true });
	addEventListener('focusin', (event) => {
		if (event.target instanceof Element && event.target.closest('header.header')) {
			root.removeAttribute('data-header-hidden');
		}
	});
})();
