// set in .env (API_BASE=...), compiled into env.js by gen-env.sh - see
// that file. not wired for prod yet: mixed-content rules mean this page
// (served over https on vercel) can't call a plain http backend, so the
// backend needs TLS in front (e.g. caddy) before a real API_BASE works.
const API_BASE = window.ENV.API_BASE;

// ---- tabs ----

const tabs = document.querySelectorAll(".tab");
const views = document.querySelectorAll(".view");

tabs.forEach((tab) => {
	tab.addEventListener("click", () => {
		tabs.forEach((t) => t.classList.remove("active"));
		views.forEach((v) => v.classList.remove("active"));
		tab.classList.add("active");
		document.getElementById(`view-${tab.dataset.view}`).classList.add("active");
	});
});

// ---- traffic dial ----

const slider = document.getElementById("dial-slider");
const readout = document.getElementById("dial-readout");

function setDial(value) {
	readout.textContent = `${value} req/s`;
	fetch(`${API_BASE}/api/traffic/dial`, {
		method: "POST",
		body: JSON.stringify({ value: Number(value) }),
	}).catch(() => {
		readout.textContent = `${value} req/s (unreachable)`;
	});
}

let debounceTimer;
slider.addEventListener("input", () => {
	readout.textContent = `${slider.value} req/s`;
	clearTimeout(debounceTimer);
	debounceTimer = setTimeout(() => setDial(slider.value), 150);
});

(async function initDial() {
	try {
		const res = await fetch(`${API_BASE}/api/traffic/dial`);
		const data = await res.json();
		slider.value = data.value;
		readout.textContent = `${data.value} req/s`;
	} catch {
		readout.textContent = "unreachable";
	}
})();

// ---- live replication stats (SSE) ----

const totalEl = document.getElementById("total-writes");
const rateEl = document.getElementById("rate-number");

function connectStats() {
	const es = new EventSource(`${API_BASE}/api/stats/stream`);
	es.onmessage = (e) => {
		const { totalWrites, writesPerSec } = JSON.parse(e.data);
		totalEl.textContent = totalWrites.toLocaleString();
		rateEl.textContent = writesPerSec.toLocaleString();
		pulse.push(writesPerSec);
	};
	es.onerror = () => {
		es.close();
		setTimeout(connectStats, 3000); // backend restarted or network blip - keep retrying
	};
}
connectStats();

// ---- pulse visualization ----
// scrolling waveform of writes/sec, like a heart-rate monitor - the more
// alive the line, the more visibly the database is being streamed.

const canvas = document.getElementById("pulse");
const ctx = canvas.getContext("2d");
const history = [];
const maxPoints = 120;

function resizeCanvas() {
	const rect = canvas.parentElement.getBoundingClientRect();
	const dpr = window.devicePixelRatio || 1;
	canvas.width = rect.width * dpr;
	canvas.height = rect.height * dpr;
	ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
}
window.addEventListener("resize", resizeCanvas);
resizeCanvas();

const pulse = {
	push(value) {
		history.push(value);
		if (history.length > maxPoints) history.shift();
	},
};

function draw() {
	const rect = canvas.parentElement.getBoundingClientRect();
	const w = rect.width;
	const h = rect.height;
	ctx.clearRect(0, 0, w, h);

	if (history.length > 1) {
		const max = Math.max(...history, 50);
		const stepX = w / (maxPoints - 1);
		const offset = maxPoints - history.length;

		ctx.beginPath();
		history.forEach((v, i) => {
			const x = (offset + i) * stepX;
			const y = h - (v / max) * (h * 0.7) - h * 0.1;
			if (i === 0) ctx.moveTo(x, y);
			else ctx.lineTo(x, y);
		});

		const last = history[history.length - 1];
		const hot = last > max * 0.75;
		ctx.strokeStyle = hot
			? getComputedStyle(document.documentElement).getPropertyValue("--accent-2")
			: getComputedStyle(document.documentElement).getPropertyValue("--accent-1");
		ctx.lineWidth = 2;
		ctx.shadowBlur = 12;
		ctx.shadowColor = ctx.strokeStyle;
		ctx.stroke();
		ctx.shadowBlur = 0;
	}

	requestAnimationFrame(draw);
}
draw();
