"use strict";

// Forms work without JavaScript. This adds selection feedback and lazy charts.
const selectionForm = document.querySelector(".selection-form");
if (selectionForm) {
  const boxes = Array.from(selectionForm.querySelectorAll('input[name="id"]'));
  const all = document.querySelector("#select-all");
  const update = () => {
    const count = boxes.filter(box => box.checked).length;
    document.querySelector("#selection-count").textContent = count ? `${count} selected` : "";
    selectionForm.querySelectorAll("[data-needs-selection]").forEach(button => { button.disabled = count === 0; });
    all.checked = boxes.length > 0 && count === boxes.length;
    all.indeterminate = count > 0 && count < boxes.length;
    all.disabled = boxes.length === 0;
  };
  all.addEventListener("change", () => { boxes.forEach(box => { box.checked = all.checked; }); update(); });
  boxes.forEach(box => box.addEventListener("change", update));
  update();
}
document.querySelectorAll("form").forEach(form => {
  form.addEventListener("submit", event => {
    if (!event.submitter || !event.submitter.hasAttribute("data-confirm-delete")) return;
    const count = form.querySelectorAll('input[name="id"]:checked, input[name="id"][type="hidden"]').length;
    if (!window.confirm(`Delete ${count} selected request${count === 1 ? "" : "s"}? This removes the stored logs permanently.`)) event.preventDefault();
  });
});

const analysis = document.querySelector(".analysis");
if (analysis) {
  let loaded = false;
  analysis.addEventListener("toggle", () => {
    if (!analysis.open || loaded) return;
    loaded = true;
    analysis.querySelectorAll("[data-chart]").forEach(chart => loadChart(chart));
  });
}

async function loadChart(chart) {
  const content = chart.querySelector(".chart-content");
  content.textContent = "Loading chart...";
  try {
    const response = await fetch(`${analysis.dataset.chartBase}/charts/${chart.dataset.chart}?${analysis.dataset.chartQuery}`, { credentials: "same-origin", headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error("Chart unavailable");
    const rows = await response.json();
    if (!Array.isArray(rows)) throw new Error("Invalid chart response");
    const kind = chart.dataset.chart;
    const points = rows.map(row => ({ label: String(kind === "status" ? row.status : row.day), value: Number(kind === "sql" ? row.average_sql : row.count) })).filter(point => Number.isFinite(point.value) && point.value >= 0);
    content.replaceChildren();
    if (!points.length) { content.textContent = "No data for these filters."; return; }
    renderChart(content, points, kind);
  } catch (_) {
    content.textContent = "Chart unavailable. ";
    content.classList.add("chart-error");
    const retry = document.createElement("button");
    retry.type = "button"; retry.className = "secondary"; retry.textContent = "Retry";
    retry.addEventListener("click", () => { content.classList.remove("chart-error"); loadChart(chart); });
    content.append(retry);
  }
}

function svgElement(tag, attributes) {
  const element = document.createElementNS("http://www.w3.org/2000/svg", tag);
  Object.entries(attributes).forEach(([name, value]) => element.setAttribute(name, String(value)));
  return element;
}

function renderChart(container, points, kind) {
  const svg = svgElement("svg", { viewBox: "0 0 340 150", role: "img", "aria-label": kind === "sql" ? "Average SQL queries for each UTC day" : kind === "status" ? "Request counts for each status code" : "Request counts for each UTC day" });
  const left = 32, top = 18, width = 298, height = 105;
  const max = Math.max(1, ...points.map(point => point.value));
  svg.append(svgElement("line", { x1: left, y1: top + height, x2: left + width, y2: top + height, class: "chart-axis" }));
  const maxText = svgElement("text", { x: left - 5, y: top + 4, "text-anchor": "end" });
  maxText.textContent = formatValue(max); svg.append(maxText);
  const step = width / points.length;
  if (kind === "status") {
    points.forEach((point, index) => {
      const bar = svgElement("rect", { x: left + step * index + step * .17, y: top + height * (1 - point.value / max), width: Math.max(1, step * .66), height: height * point.value / max, class: "chart-bar" });
      const title = svgElement("title", {}); title.textContent = `${point.label}: ${formatValue(point.value)}`; bar.append(title); svg.append(bar);
    });
  } else {
    const path = points.map((point, index) => `${index ? "L" : "M"}${left + (points.length === 1 ? width / 2 : width * index / (points.length - 1))},${top + height * (1 - point.value / max)}`).join(" ");
    svg.append(svgElement("path", { d: path, class: "chart-line" }));
    if (points.length === 1) svg.append(svgElement("circle", { cx: left + width / 2, cy: top + height * (1 - points[0].value / max), r: 3, class: "chart-bar" }));
  }
  const labels = kind === "status" && points.length <= 8 ? points.map((point, index) => ({ point, x: left + step * (index + .5), anchor: "middle" })) : [{ point: points[0], x: left, anchor: "start" }, ...(points.length > 1 ? [{ point: points[points.length - 1], x: left + width, anchor: "end" }] : [])];
  labels.forEach(label => { const text = svgElement("text", { x: label.x, y: 143, "text-anchor": label.anchor }); text.textContent = label.point.label; svg.append(text); });
  container.append(svg);
  const details = document.createElement("details");
  const summary = document.createElement("summary"); summary.textContent = "View chart data"; details.append(summary);
  const table = document.createElement("table");
  const caption = document.createElement("caption"); caption.className = "sr-only"; caption.textContent = svg.getAttribute("aria-label"); table.append(caption);
  const header = document.createElement("tr");
  [kind === "status" ? "Status" : "UTC day", kind === "sql" ? "Average queries" : "Requests"].forEach(label => { const th = document.createElement("th"); th.scope = "col"; th.textContent = label; header.append(th); });
  const head = document.createElement("thead"); head.append(header); table.append(head);
  const body = document.createElement("tbody");
  points.forEach(point => { const row = document.createElement("tr"); [point.label, formatValue(point.value)].forEach(value => { const cell = document.createElement("td"); cell.textContent = value; row.append(cell); }); body.append(row); });
  table.append(body); details.append(table); container.append(details);
}

function formatValue(value) { return new Intl.NumberFormat(undefined, { maximumFractionDigits: 2, notation: value > 10000 ? "compact" : "standard" }).format(value); }
