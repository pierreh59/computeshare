// Ecran fournisseur : stats temps reel + controle du noeud.
(async () => {
  const app = document.querySelector("#app");
  try {
    if (!window.__TAURI__) throw new Error("API Tauri indisponible (withGlobalTauri ou capabilities manquants)");
    const inv = window.__TAURI__.core.invoke;
const inv = window.__TAURI__.core.invoke;
const hw = await inv("detect_hardware");
const nodeId = "node-" + Math.random().toString(16).slice(2, 8);

document.querySelector("#app").innerHTML = `
  <h1>ComputeShare</h1>
  <p>${hw.gpu_model} (${hw.gpu_category}) - ${hw.cpu_cores} coeurs - ${hw.ram_gb} Go RAM</p>
  <div style="display:flex;gap:12px;flex-wrap:wrap;margin-top:16px">
    <div class="cs-stat"><div class="cs-stat-label">Gains cumules</div>
      <div class="cs-stat-value" id="earn">0.0000 EUR</div></div>
    <div class="cs-stat"><div class="cs-stat-label">Charge GPU</div>
      <div class="cs-stat-value" id="load">0 %</div></div>
    <div class="cs-stat"><div class="cs-stat-label">Temperature GPU</div>
      <div class="cs-stat-value" id="temp">0 C</div></div>
    <div class="cs-stat"><div class="cs-stat-label">Statut minage</div>
      <div class="cs-stat-value" id="mine">PAUSE</div></div>
  </div>
  <div style="margin-top:16px">
    <input id="gw" value="http://localhost:8080" size="24">
    <button id="start">Demarrer le noeud (${nodeId})</button>
    <span id="nodeinfo" style="margin-left:8px"></span>
  </div>
  <p id="state" style="margin-top:16px">...</p>
`;

document.querySelector("#start").onclick = async () => {
  try {
    await inv("start_node", {
      gateway: document.querySelector("#gw").value,
      nodeId: nodeId,
    });
    document.querySelector("#start").disabled = true;
    document.querySelector("#start").textContent = "Noeud demarre (" + nodeId + ")";
  } catch (e) {
    alert("Echec du demarrage : " + e);
  }
};

async function refresh() {
  const s = await inv("get_live_stats");
  document.querySelector("#earn").textContent = s.earnings_eur.toFixed(4) + " EUR";
  document.querySelector("#load").textContent = s.gpu_load + " %";
  document.querySelector("#temp").textContent = s.gpu_temp_c + " C";
  document.querySelector("#mine").textContent = s.should_mine ? "ACTIF" : "PAUSE";
  document.querySelector("#state").textContent =
    "CPU " + s.cpu_load.toFixed(0) + " % - uptime " + s.uptime_secs + " s";
  try {
    const n = await inv("node_status");
    document.querySelector("#nodeinfo").textContent = n.running
      ? "en ligne - " + n.chunks_done + " ok / " + n.chunks_failed + " echecs" +
        (n.last_error ? " - " + n.last_error : "")
      : "arrete";
  } catch (e) { /* node_status pas dispo */ }
}
refresh();
setInterval(refresh, 2000);
  } catch (e) {
    app.innerHTML = "<h1>ComputeShare</h1><p style='color:#f87171'>Erreur au chargement : " + e + "</p>";
  }
})();
