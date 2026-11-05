// Sprint 16 (LAI-62) - App fournisseur zero configuration.
// Ecran de connexion (e-mail + mot de passe du compte cree sur
// computeshare.fr), puis tableau de bord : le jeton de noeud est
// recupere automatiquement, le noeud demarre tout seul.
(async () => {
  const app = document.querySelector("#app");
  try {
    if (!window.__TAURI__) throw new Error("API Tauri indisponible");
    const inv = window.__TAURI__.core.invoke;

    function esc(s) {
      const d = document.createElement("div");
      d.textContent = s == null ? "" : String(s);
      return d.innerHTML;
    }

    const st = await inv("auth_status");

    if (!st.logged_in) {
      // ---------- ECRAN CONNEXION ----------
      app.innerHTML = `
        <h1>ComputeShare</h1>
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
      <h1>ComputeShare <span style="font-size:13px;color:#94a3b8">— ${esc(st.email)} (${esc(st.node_id)})</span></h1>
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
      } catch (e) { /* rafraichissement suivant */ }
    }
    refresh();
    setInterval(refresh, 2000);
  } catch (e) {
    app.innerHTML = "<h1>ComputeShare</h1><p style='color:#f87171'>Erreur au chargement : " + e + "</p>";
  }
})();
