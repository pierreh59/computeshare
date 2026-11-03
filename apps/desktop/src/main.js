// Ecran temps reel fournisseur : gains, charge GPU, temperature, statut.
const inv = window.__TAURI__.core.invoke;
const hw = await inv("detect_hardware");

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
  <p id="state" style="margin-top:16px">...</p>
`;

async function refresh() {
  const s = await inv("get_live_stats");
  document.querySelector("#earn").textContent = s.earnings_eur.toFixed(4) + " EUR";
  document.querySelector("#load").textContent = s.gpu_load + " %";
  document.querySelector("#temp").textContent = s.gpu_temp_c + " C";
  document.querySelector("#mine").textContent = s.should_mine ? "ACTIF" : "PAUSE";
  document.querySelector("#state").textContent =
    "CPU " + s.cpu_load.toFixed(0) + " % - uptime " + s.uptime_secs + " s";
}
refresh();
setInterval(refresh, 2000);
