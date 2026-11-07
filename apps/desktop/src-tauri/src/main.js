// Sprint 16 (LAI-62) - App fournisseur zero configuration.
// Ecran de connexion (e-mail + mot de passe du compte cree sur
// computeshare.fr), puis tableau de bord : le jeton de noeud est
// recupere automatiquement, le noeud demarre tout seul.
(async () => {
  const app = document.querySelector("#app");
  try {
    if (!window.__TAURI__) throw new Error("API Tauri indisponible");
    const inv = window.__TAURI__.core.invoke;

    // Version de l'app, affichee dans l'en-tete (pour verifier d'un
    // coup d'oeil quelle version tourne, surtout apres une maj auto).
    let ver = "";
    try { ver = await inv("plugin:app|version"); } catch (e) { ver = ""; }

    function esc(s) {
      const d = document.createElement("div");
      d.textContent = s == null ? "" : String(s);
      return d.innerHTML;
    }

    const st = await inv("auth_status");

    if (!st.logged_in) {
      // ---------- ECRAN CONNEXION ----------
      app.innerHTML = `
        <h1>ComputeShare${ver ? ' <span style="font-size:13px;color:#94a3b8">v' + esc(ver) + '</span>' : ""}</h1>
        <p>Connectez-vous avec votre compte fournisseur pour proposer la puissance de votre machine.</p>
        <div class="cs-card" style="max-width:420px">
          <label>E-mail</label><br>
          <input id="email" type="email" size="28" value="${esc(st.email)}" placeholder="vous@exemple.fr" autofocus><br>
          <label>Mot de passe</label><br>
          <input id="password" type="password" size="28"><br><br>
          <button id="login">Se connecter</button>
          <span id="loginmsg" style="margin-left:10px"></span>
        </div>
        <p style="margin-top:14px">Pas encore de compte fournisseur ? <a href="#" id="signup">Créer un compte sur computeshare.fr</a></p>
        <details style="margin-top:18px;max-width:420px"><summary style="color:#94a3b8;cursor:pointer">Paramètres avancés (réservé aux tests)</summary>
          <label>Adresse de la plateforme</label><br>
          <input id="gateway" size="34" value="${esc(st.gateway || "https://api.computeshare.fr")}">
        </details>`;
      document.querySelector("#signup").onclick = (e) => {
        e.preventDefault();
        inv("plugin:opener|open_url", { url: "https://computeshare.fr/signup" }).catch(() => {});
      };
      const doLogin = async () => {
        const msg = document.querySelector("#loginmsg");
        msg.textContent = "Connexion...";
        try {
          const gateway = document.querySelector("#gateway")
            ? document.querySelector("#gateway").value.trim()
            : "";
          const r = await inv("login", {
            gateway: gateway,
            email: document.querySelector("#email").value.trim(),
            password: document.querySelector("#password").value,
          });
          msg.textContent = "Connecté !";
          location.reload();
        } catch (e) {
          msg.innerHTML = '<span style="color:#f87171">' + esc(String(e)) + "</span>";
        }
      };
      document.querySelector("#login").onclick = doLogin;
      document.querySelector("#password").addEventListener("keydown", (e) => {
        if (e.key === "Enter") doLogin();
      });
      return;
    }

    // ---------- TABLEAU DE BORD ----------
    const hw = await inv("detect_hardware");
    let auto = false;
    try { auto = await inv("plugin:autostart|is_enabled"); } catch (e) { auto = false; }

    app.innerHTML = `
      <h1>ComputeShare <span style="font-size:13px;color:#94a3b8">— ${esc(st.email)} (${esc(st.node_id)})${ver ? " — v" + esc(ver) : ""}</span></h1>
      <p>${esc(hw.gpu_model)} (${esc(hw.gpu_category)}) — ${hw.cpu_cores} cœurs — ${hw.ram_gb} Go RAM</p>
      <div style="display:flex;gap:12px;flex-wrap:wrap;margin-top:16px">
        <div class="cs-stat"><div class="cs-stat-label">Gains cumulés</div>
          <div class="cs-stat-value" id="earn">0.0000 EUR</div></div>
        <div class="cs-stat"><div class="cs-stat-label">Charge GPU</div>
          <div class="cs-stat-value" id="load">0 %</div></div>
        <div class="cs-stat"><div class="cs-stat-label">Température GPU</div>
          <div class="cs-stat-value" id="temp">0 C</div></div>
        <div class="cs-stat"><div class="cs-stat-label">Statut minage</div>
          <div class="cs-stat-value" id="mine">PAUSE</div></div>
      </div>
      <p id="state" style="margin-top:16px">...</p>
      <div class="cs-card" style="margin-top:10px;max-width:640px">
        <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
          <button id="availbtn" style="font-weight:bold">...</button>
          <span id="availmsg" style="font-size:13px;color:#94a3b8"></span>
        </div>
        <canvas id="actgraph" width="620" height="140" style="margin-top:12px;width:100%;max-width:620px;background:#0f172a;border-radius:8px"></canvas>
        <div style="margin-top:6px;font-size:12px;color:#94a3b8">
          <span style="color:#60a5fa">&#9644; CPU</span> &nbsp;
          <span style="color:#4ade80">&#9644; GPU</span> &nbsp;
          <span style="color:#f59e0b">&#9644; ComputeShare au travail</span> &nbsp; (1 derniere heure)
        </div>
      </div>
      <div class="cs-card" style="margin-top:10px;max-width:640px">
        <label><input type="checkbox" id="autostart" ${auto ? "checked" : ""}> Lancer ComputeShare au démarrage de Windows</label>
        <span id="autoerr" style="color:#f87171;margin-left:8px"></span>
        <div style="margin-top:12px">
          <button id="update">Rechercher les mises à jour</button>
          <span id="upmsg" style="margin-left:10px"></span>
          <button id="logout" style="margin-left:16px">Se déconnecter</button>
        </div>
      </div>`;

    // Demarrage automatique du noeud : zero clic.
    try {
      await inv("start_node");
    } catch (e) {
      document.querySelector("#state").textContent = String(e);
    }

    document.querySelector("#autostart").onchange = async (e) => {
      try {
        if (e.target.checked) await inv("plugin:autostart|enable");
        else await inv("plugin:autostart|disable");
        document.querySelector("#autoerr").textContent = "";
      } catch (err) {
        document.querySelector("#autoerr").textContent = String(err);
      }
    };

    document.querySelector("#logout").onclick = async (e) => {
      // Confirmation en deux clics (pas de boite de dialogue native,
      // qui s'affiche chez le client sous le titre "tauri.localhost").
      const b = e.target;
      if (b.dataset.armed !== "1") {
        b.dataset.armed = "1";
        b.textContent = "Confirmer la déconnexion";
        setTimeout(() => { b.dataset.armed = ""; b.textContent = "Se déconnecter"; }, 4000);
        return;
      }
      await inv("logout");
      location.reload();
    };

    document.querySelector("#update").onclick = async () => {
      const msg = document.querySelector("#upmsg");
      msg.textContent = "Vérification...";
      try {
        const u = await inv("plugin:updater|check");
        if (u && u.rid !== undefined) {
          // Proposition inline : boutons dans la fenetre, pas de
          // confirm() natif (titre "tauri.localhost indique").
          msg.textContent = "";
          const span = document.createElement("span");
          span.textContent = "Nouvelle version " + (u.version || "?") + " disponible — ";
          const btnOui = document.createElement("button");
          btnOui.textContent = "Installer maintenant";
          const btnNon = document.createElement("button");
          btnNon.textContent = "Plus tard";
          btnNon.style.marginLeft = "8px";
          span.appendChild(btnOui);
          span.appendChild(btnNon);
          msg.appendChild(span);
          const nettoyer = () => { msg.textContent = ""; };
          btnNon.onclick = nettoyer;
          btnOui.onclick = async () => {
            nettoyer();
            const Channel = window.__TAURI__.core.Channel;
            if (!Channel) {
              msg.innerHTML = '<span style="color:#f87171">Interface de mise à jour indisponible.</span>';
              return;
            }
            msg.textContent = "Téléchargement de la mise à jour... 0 %";
            try {
              let recu = 0, total = 0;
              const chan = new Channel();
              chan.onmessage = (ev) => {
                if (ev.event === "Started" && ev.data && ev.data.contentLength) {
                  total = ev.data.contentLength;
                } else if (ev.event === "Progress") {
                  recu += ev.data.chunkLength;
                  msg.textContent = "Téléchargement de la mise à jour... " +
                    (total ? Math.round(recu / total * 100) + " %" : Math.round(recu / 1024) + " Ko");
                } else if (ev.event === "Finished") {
                  msg.textContent = "Installation... l'application va redémarrer.";
                }
              };
              await inv("plugin:updater|download_and_install", { rid: u.rid, onEvent: chan });
              await inv("plugin:process|relaunch");
            } catch (err) {
              msg.innerHTML = '<span style="color:#f87171">' + esc(String(err)) + "</span>";
            }
          };
        } else {
          msg.textContent = "Application à jour.";
        }
      } catch (e) {
        msg.innerHTML = '<span style="color:#f87171">' + esc(String(e)) + "</span>";
      }
    };

    // Sprint 21 : interrupteur manuel Disponible / En pause.
    let manualOn = true;
    try { const a0 = await inv("get_activity"); manualOn = a0.manual_on; } catch (e) {}
    const availBtn = document.querySelector("#availbtn");
    const renderAvailBtn = () => {
      availBtn.textContent = manualOn ? "\u23F8 Mettre en pause" : "\u25B6 Remettre disponible";
      availBtn.style.background = manualOn ? "#1e293b" : "#166534";
      availBtn.style.color = manualOn ? "#e2e8f0" : "#ffffff";
    };
    renderAvailBtn();
    availBtn.onclick = async () => {
      manualOn = !manualOn;
      renderAvailBtn();
      try { await inv("set_available", { on: manualOn }); }
      catch (err) { manualOn = !manualOn; renderAvailBtn(); }
    };

    // Sprint 21 : graphe d'activite (CPU, GPU, periodes de travail).
    const drawActivity = (act) => {
      const cv = document.querySelector("#actgraph");
      if (!cv) return;
      const g = cv.getContext("2d");
      const W = cv.width, H = cv.height;
      g.clearRect(0, 0, W, H);
      g.strokeStyle = "#1e293b";
      g.lineWidth = 1;
      for (let y = 0; y <= 4; y++) {
        const py = Math.round(y * H / 4) + 0.5;
        g.beginPath(); g.moveTo(0, py); g.lineTo(W, py); g.stroke();
      }
      const s = act.samples || [];
      if (!s.length) return;
      const step = W / Math.max(s.length - 1, 1);
      // Fond orange sur les periodes ou un chunk tournait.
      g.fillStyle = "rgba(245,158,11,0.18)";
      for (let i = 0; i < s.length; i++) {
        if (s[i].working) {
          g.fillRect(Math.round(i * step) - step / 2, 0, Math.max(step, 1.5), H);
        }
      }
      const line = (vals, color) => {
        g.strokeStyle = color;
        g.lineWidth = 1.5;
        g.beginPath();
        for (let i = 0; i < s.length; i++) {
          const px = i * step;
          const py = H - (Math.min(vals(s[i]), 100) / 100) * (H - 2) - 1;
          if (i === 0) g.moveTo(px, py); else g.lineTo(px, py);
        }
        g.stroke();
      };
      line((x) => x.cpu, "#60a5fa");
      if (act.gpu_present) line((x) => x.gpu, "#4ade80");
      // Message de disponibilite.
      const msg = document.querySelector("#availmsg");
      const c = Math.round(act.cpu), gp = act.gpu;
      if (!act.manual_on) {
        msg.textContent = "En pause : le noeud ne prend aucun travail (bouton manuel).";
      } else if (act.available) {
        msg.textContent = "Disponible : accepte du travail (CPU " + c + "% / seuil " + Math.round(act.cpu_max) + "%, GPU " + gp + "% / seuil " + act.gpu_max + "%).";
      } else {
        msg.textContent = "En attente : charge trop elevee (CPU " + c + "% / seuil " + Math.round(act.cpu_max) + "%, GPU " + gp + "% / seuil " + act.gpu_max + "%).";
      }
    };

    async function refresh() {
      try {
        const s = await inv("get_live_stats");
        document.querySelector("#earn").textContent = s.earnings_eur.toFixed(4) + " EUR";
        document.querySelector("#load").textContent = s.gpu_load + " %";
        document.querySelector("#temp").textContent = s.gpu_temp_c + " C";
        document.querySelector("#mine").textContent = s.should_mine ? "ACTIF" : "PAUSE";
        document.querySelector("#state").textContent =
          "CPU " + s.cpu_load.toFixed(0) + " % — uptime " + s.uptime_secs + " s";
        const n = await inv("node_status");
        document.querySelector("#state").textContent +=
          n.running
            ? " — noeud en ligne : " + n.chunks_done + " ok / " + n.chunks_failed + " échecs" +
              (n.last_error ? " — " + n.last_error : "")
            : " — noeud arrêté";
        try { drawActivity(await inv("get_activity")); } catch (e) { /* graphe au suivant */ }
      } catch (e) { /* rafraichissement suivant */ }
    }
    refresh();
    setInterval(refresh, 2000);
  } catch (e) {
    app.innerHTML = "<h1>ComputeShare</h1><p style='color:#f87171'>Erreur au chargement : " + e + "</p>";
  }
})();
