import { mountAppearanceShop } from "/appearance-shop.js";
// In-game incense shop. Runs on the temple page, which is plain HTML plus the
// archived Three.js runtime, so this stays dependency-free and never touches
// the scene. It talks to the same /api/economy endpoints as /shop.
const text = JSON.parse(document.getElementById("sansa-shop-text")?.textContent || "{}");
const overlay = document.getElementById("sansa-shop-overlay");
const body = document.getElementById("sansa-shop-body");
const entry = document.querySelector(".shop-entry");

const display = {
	ko: ["KRW", 1780], zh: ["CNY", 9.3], ja: ["JPY", 195], en: ["USD", 1.27],
	es: ["EUR", 1.2], fr: ["EUR", 1.2], "zh-Hant": ["TWD", 43.9],
};
const money = (cents) => {
	const [currency, rate] = display[document.documentElement.lang] || ["GBP", 1];
	const amount = new Intl.NumberFormat(text.locale || "en-GB", {
		style: "currency",
		currency,
		maximumFractionDigits: 0,
	}).format(Math.ceil((cents / 100) * rate));
	return currency === "GBP" ? amount : `≈ ${amount}`;
};
const esc = (value) =>
	String(value ?? "").replace(
		/[&<>"']/g,
		(c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c],
	);

let disposeAppearance;
let me = null;
let notice = "";
let busy = false;

const templePath = () => {
	const path = location.pathname.replace(/\/$/, "");
	return /^\/(?:ko|zh|ja|en|es|fr|zh-Hant)?$/.test(path) ? path || "/" : "";
};

const post = async (url, data) => {
	const response = await fetch(url, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(data),
	}).catch(() => null);
	return { status: response?.status ?? 0, data: await response?.json().catch(() => ({})) };
};

function render() {
	disposeAppearance?.();
	if (!body) return;
	if (!me) {
		body.innerHTML = `<p class="shop-fine">${esc(text.paymentClosed)}</p>`;
		return;
	}
	const owned = me.entitlements?.some((entry) => entry.itemId === "lantern-name");
	const parts = [];

	if (notice) parts.push(`<p class="shop-notice">${esc(notice)}</p>`);

	parts.push(
		`<div class="shop-balance"><span class="shop-incense">${me.signedIn ? me.incense : "—"}</span><span>${esc(text.balance)}</span></div>`,
	);

	if (!me.signedIn) {
		parts.push(`<section class="shop-signin"><strong>${esc(text.signIn)}</strong>
			<p>${esc(text.signInBody)}</p>
			<button type="button" data-act="signin" ${me.signIn?.ready && me.signIn?.google ? "" : "disabled"}>${esc(text.signIn)}</button>
			${me.signIn?.ready && me.signIn?.google ? "" : `<small>${esc(text.paymentClosed)}</small>`}
		</section>`);
	}

	parts.push(`<section id="halo-shop" class="appearance-shop"></section>`);
	parts.push(`<h2>${esc(text.packs)}</h2><div class="shop-packs">`);
	for (const pack of me.packs || []) {
		parts.push(`<button class="shop-pack" type="button" data-pack="${esc(pack.id)}" ${me.signedIn && me.payments && !busy ? "" : "disabled"}>
			<strong>${pack.incense}</strong><span>${esc(pack.label?.[document.documentElement.lang] || "")}</span><em>${money(pack.priceCents)}</em>
		</button>`);
	}
	parts.push("</div>");

	const item = (me.items || []).find((entry) => entry.id === "lantern-name");
	if (item) {
		parts.push(`<h2>${esc(text.item)}</h2><p class="shop-fine">${esc(text.itemBody)}</p><div class="shop-item">
			<div><strong>${esc(item.name?.[document.documentElement.lang] || text.item)}</strong><span>${item.incense} ${esc(text.unit || "")}</span></div>
			${
				owned
					? `<span class="shop-owned">${esc(text.owned)}</span>`
					: `<div class="shop-claim"><label>${esc(text.name)}<input maxlength="24" placeholder="${esc(text.namePlaceholder)}" data-name></label><button type="button" data-act="claim" ${busy ? "disabled" : ""}>${esc(text.claim)}</button></div>`
			}
		</div>`);
	}

	parts.push(`<p class="shop-fine">${esc(text.note)}</p>`);
	body.innerHTML = parts.join("");
	disposeAppearance = mountAppearanceShop(body.querySelector("#halo-shop"), {
		state: me, language: document.documentElement.lang, scene: true,
		reload: load, signIn: () => act("signin"), preview: () => close(),
	});
}

async function load() {
	const response = await fetch("/api/economy/me").catch(() => null);
	me = response?.ok ? await response.json() : null;
	render();
}

async function syncAndOpen() {
	const params = new URLSearchParams(location.search);
	const sessionId = params.get("session_id");
	if (params.get("shop") === "success" && sessionId) {
		await fetch(`/api/economy/checkout/sync?session_id=${encodeURIComponent(sessionId)}`).catch(
			() => null,
		);
		notice = text.success;
		history.replaceState({}, "", location.pathname);
	}
	open();
}

function open() {
	if (!overlay) return;
	overlay.hidden = false;
	document.documentElement.classList.add("shop-open");
	overlay.querySelector(".shop-close")?.focus();
	load();
}

function close() {
	if (!overlay) return;
	overlay.hidden = true;
	document.documentElement.classList.remove("shop-open");
	entry?.focus();
}

async function act(name, target) {
	if (busy) return;
	if (name === "signin") {
		const { data } = await post("/api/auth/sign-in/social", {
			provider: "google",
			callbackURL: location.href,
		});
		if (data?.url) location.href = data.url;
		return;
	}
	if (name === "buy" && target) {
		busy = true;
		const packId = target.getAttribute("data-pack");
		const query = new URLSearchParams({ locale: document.documentElement.lang });
		const back = templePath();
		if (back) query.set("return", back);
		const { data } = await post(`/api/economy/checkout?${query}`, { packId });
		busy = false;
		if (data?.url) {
			location.href = data.url;
			return;
		}
		notice = data?.error === "sign-in-required" ? text.signIn : text.paymentClosed;
		render();
		return;
	}
	if (name === "claim") {
		busy = true;
		const input = body?.querySelector("[data-name]");
		const { data } = await post("/api/economy/spend", {
			itemId: "lantern-name",
			name: input?.value || "",
		});
		busy = false;
		if (data?.error === "insufficient-incense") notice = text.insufficient;
		else if (data?.error === "invalid-name") notice = text.name;
		else notice = "";
		await load();
	}
}

entry?.addEventListener("click", open);
overlay?.addEventListener("click", (event) => {
	const closeTarget = event.target.closest("[data-shop-close]");
	if (closeTarget) {
		close();
		return;
	}
	const pack = event.target.closest("[data-pack]");
	if (pack) {
		act("buy", pack);
		return;
	}
	const action = event.target.closest("[data-act]");
	if (action) act(action.getAttribute("data-act"), action);
});
document.addEventListener("keydown", (event) => {
	if (event.key === "Escape" && overlay && !overlay.hidden) close();
});



function mountControls() {
	const glassTools = document.querySelector("nav.game-tools");
	const templeControls = document.querySelector(".temple-controls");
	if (!glassTools || !templeControls) return false;
	if (templeControls.parentElement === glassTools) return true;
	glassTools.prepend(templeControls);
	return true;
}
if (!mountControls()) {
	let tries = 0;
	const timer = setInterval(() => {
		if (mountControls() || ++tries > 40) clearInterval(timer);
	}, 250);
}

const langToggle = document.querySelector(".lang-toggle");
const langList = document.querySelector(".lang-list");
langToggle?.addEventListener("click", (event) => {
	event.stopPropagation();
	if (!langList) return;
	const open = langList.hidden;
	langList.hidden = !open;
	langToggle.setAttribute("aria-expanded", String(open));
});
document.addEventListener("click", (event) => {
	if (langList && !langList.hidden && !event.target.closest(".lang-pill")) {
		langList.hidden = true;
		langToggle?.setAttribute("aria-expanded", "false");
	}
});

if (new URLSearchParams(location.search).get("shop") === "success") syncAndOpen();
