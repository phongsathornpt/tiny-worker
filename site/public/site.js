(() => {
	const palette = document.querySelector("[data-palette]");
	const input = document.querySelector("[data-palette-input]");
	if (!palette || !input) return;

	const openButton = document.querySelector("[data-palette-open]");
	const closeButton = document.querySelector("[data-palette-close]");
	const links = [...palette.querySelectorAll("[data-palette-links] a")];

	function openPalette() {
		if (!palette.open) palette.showModal();
		input.value = "";
		filterLinks();
		input.focus();
	}

	function filterLinks() {
		const query = input.value.trim().toLowerCase();
		for (const link of links) {
			link.hidden = !link.textContent.toLowerCase().includes(query);
		}
	}

	openButton?.addEventListener("click", openPalette);
	closeButton?.addEventListener("click", () => palette.close());
	input.addEventListener("input", filterLinks);
	palette.addEventListener("click", (event) => {
		if (event.target === palette) palette.close();
	});
	palette.addEventListener("close", () => openButton?.focus());

	document.addEventListener("keydown", (event) => {
		if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
			e.preventDefault();
			openPalette();
		}
	});

	document.addEventListener("click", (event) => {
		const button = event.target.closest(".code-switch__button");
		if (!button) return;
		for (const item of document.querySelectorAll(".code-switch__button")) {
			item.classList.toggle("is-active", item === button);
		}
	});
})();
