// Front minimal de l'app desktop : appelle detect_hardware (Rust) et affiche.
const info = await window.__TAURI__.core.invoke("detect_hardware");
document.querySelector("#app").innerHTML = `
  <h1>ComputeShare</h1>
  <p>GPU : ${info.gpu_model} (${info.gpu_vram_mb} Mo) - categorie : <b>${info.gpu_category}</b></p>
  <p>CPU : ${info.cpu_model} - ${info.cpu_cores} coeurs - RAM : ${info.ram_gb} Go</p>
`;
