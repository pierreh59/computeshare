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
        <div class="top">
          <div class="brand">
            <div class="logo">CS</div>
            <div>
              <h1>ComputeShare${ver ? ' <span style="font-size:13px;color:#8b98b8">v' + esc(ver) + '</span>' : ""}</h1>
              <div class="sub">Proposez la puissance de votre machine.</div>
            </div>
          </div>
        </div>
        <div class="cs-card" style="max-width:430px">
          <label>E-mail</label><br>
          <input id="email" type="email" size="28" value="${esc(st.email)}" placeholder="vous@exemple.fr" autofocus><br>
          <label>Mot de passe</label><br>
          <input id="password" type="password" size="28"><br><br>
          <button id="login" class="primary">Se connecter</button>
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
      <div class="top">
        <div class="brand">
          <div class="logo">CS</div>
          <div>
            <h1>ComputeShare</h1>
            <div class="sub">${esc(st.email)} (${esc(st.node_id)})${ver ? " — v" + esc(ver) : ""}</div>
          </div>
        </div>
        <span class="pill" id="pill"><span class="dot"></span><span id="pilltxt">...</span></span>
      </div>
      <p class="hw">${esc(hw.gpu_model)} (${esc(hw.gpu_category)}) — ${hw.cpu_cores} cœurs — ${hw.ram_gb} Go RAM</p>
      <div class="stats">
        <div class="cs-stat"><div class="cs-stat-label">&#128176; Gains cumulés</div>
          <div class="cs-stat-value money" id="earn">0.0000 EUR</div></div>
        <div class="cs-stat"><div class="cs-stat-label">&#9889; Charge GPU</div>
          <div class="cs-stat-value" id="load">0 %</div></div>
        <div class="cs-stat"><div class="cs-stat-label">&#127777; Température GPU</div>
          <div class="cs-stat-value" id="temp">0 C</div></div>
        <div class="cs-stat"><div class="cs-stat-label">&#9679; Statut minage</div>
          <div class="cs-stat-value" id="mine">PAUSE</div></div>
      </div>
      <p id="state" class="state">...</p>
      <div class="cs-card card-wide">
        <div class="avail-row">
          <button id="availbtn" class="ok">...</button>
          <span id="availmsg"></span>
        </div>
        <canvas id="actgraph" width="640" height="150"></canvas>
        <div class="legend">
          <span class="chip"><span class="sw" style="background:#60a5fa"></span>CPU</span>
          <span class="chip"><span class="sw" style="background:#4ade80"></span>GPU</span>
          <span class="chip"><span class="sw" style="background:rgba(245,158,11,.45)"></span>ComputeShare au travail</span>
          <span class="chip">1 dernière heure</span>
        </div>
      </div>
      <div class="cs-card card-wide">
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
        b.className = "danger";
        b.textContent = "Confirmer la déconnexion";
        setTimeout(() => { b.dataset.armed = ""; b.className = ""; b.textContent = "Se déconnecter"; }, 4000);
        return;
      }
      await inv("logout");
      location.reload();
    };

    document.querySelector("#update").onclick = async () => {
      const msg = document.querySelector("#upmsg");
      msg.textContent = "Vérification...";
      try {
        const u = await inv("plugin:updater|check", { timeout: 15000 });
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
              await inv("plugin:updater|download_and_install", { rid: u.rid, onEvent: chan, timeout: 300000 });
              await inv("plugin:process|relaunch");
            } catch (err) {
              msg.innerHTML = '<span style="color:#f87171">' + esc(String(err)) + "</span>";
            }
          };
        } else {
          msg.textContent = "Application à jour.";
        }
      } catch (e) {
        // Sprint 28b : message clair au lieu d'un "Vérification..." eternel
        // (la verification n'a plus de timeout infini, mais on guide l'utilisateur).
        msg.innerHTML = '<span style="color:#f87171">Échec de la vérification : ' +
          esc(String(e)) +
          ' (si cela persiste, téléchargez la mise à jour manuellement sur github.com/pierreh59/computeshare/releases)</span>';
      }
    };

    // Sprint 21 : interrupteur manuel Disponible / En pause.
    let manualOn = true;
    try { const a0 = await inv("get_activity"); manualOn = a0.manual_on; } catch (e) {}
    const availBtn = document.querySelector("#availbtn");
    const renderAvailBtn = () => {
      availBtn.textContent = manualOn ? "\u23F8 Mettre en pause" : "\u25B6 Remettre disponible";
      availBtn.className = manualOn ? "ok" : "primary";
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
      // Grille horizontale.
      g.strokeStyle = "rgba(148,163,184,0.12)";
      g.lineWidth = 1;
      for (let y = 0; y <= 4; y++) {
        const py = Math.round(y * H / 4) + 0.5;
        g.beginPath(); g.moveTo(0, py); g.lineTo(W, py); g.stroke();
      }
      const s = act.samples || [];
      // Pastille d'etat de l'entete + message de disponibilite.
      const pill = document.querySelector("#pill");
      const pilltxt = document.querySelector("#pilltxt");
      const msg = document.querySelector("#availmsg");
      const c = Math.round(act.cpu), gp = act.gpu;
      if (!act.manual_on) {
        if (pill) pill.className = "pill off";
        if (pilltxt) pilltxt.textContent = "En pause (manuel)";
        if (msg) msg.textContent = "En pause : le noeud ne prend aucun travail (bouton manuel).";
      } else if (act.available) {
        if (pill) pill.className = "pill on";
        if (pilltxt) pilltxt.textContent = "Disponible";
        if (msg) msg.textContent = "Disponible : accepte du travail (CPU " + c + "% / seuil " + Math.round(act.cpu_max) + "%, GPU " + gp + "% / seuil " + act.gpu_max + "%).";
      } else {
        if (pill) pill.className = "pill wait";
        if (pilltxt) pilltxt.textContent = "En attente";
        if (msg) msg.textContent = "En attente : charge trop elevee (CPU " + c + "% / seuil " + Math.round(act.cpu_max) + "%, GPU " + gp + "% / seuil " + act.gpu_max + "%).";
      }
      if (!s.length) return;
      const step = W / Math.max(s.length - 1, 1);
      // Fond orange sur les periodes ou un chunk tournait.
      g.fillStyle = "rgba(245,158,11,0.14)";
      for (let i = 0; i < s.length; i++) {
        if (s[i].working) {
          g.fillRect(Math.round(i * step) - step / 2, 0, Math.max(step, 1.5), H);
        }
      }
      // Courbe remplie + ligne, avec seuil en pointilles.
      const series = (vals, color, max) => {
        // seuil (pointilles) au niveau du max autorise.
        g.strokeStyle = color;
        g.globalAlpha = 0.35;
        g.setLineDash([4, 5]);
        g.lineWidth = 1;
        const ty = H - (Math.min(max, 100) / 100) * (H - 2) - 1;
        g.beginPath(); g.moveTo(0, ty); g.lineTo(W, ty); g.stroke();
        g.setLineDash([]);
        g.globalAlpha = 1;
        // aire sous la courbe.
        g.beginPath();
        for (let i = 0; i < s.length; i++) {
          const px = i * step;
          const py = H - (Math.min(vals(s[i]), 100) / 100) * (H - 2) - 1;
          if (i === 0) g.moveTo(px, py); else g.lineTo(px, py);
        }
        g.lineTo(W, H); g.lineTo(0, H); g.closePath();
        const grad = g.createLinearGradient(0, 0, 0, H);
        grad.addColorStop(0, color.replace("0.9", "0.28"));
        grad.addColorStop(1, "rgba(0,0,0,0)");
        g.globalAlpha = 1;
        g.fillStyle = grad;
        g.fill();
        // ligne par-dessus.
        g.globalAlpha = 1;
        g.strokeStyle = color;
        g.lineWidth = 1.6;
        g.beginPath();
        for (let i = 0; i < s.length; i++) {
          const px = i * step;
          const py = H - (Math.min(vals(s[i]), 100) / 100) * (H - 2) - 1;
          if (i === 0) g.moveTo(px, py); else g.lineTo(px, py);
        }
        g.stroke();
      };
      series((x) => x.cpu, "rgba(96,165,250,0.9)", act.cpu_max);
      if (act.gpu_present) series((x) => x.gpu, "rgba(74,222,128,0.9)", act.gpu_max);
    };

    async function refresh() {
      try {
        const s = await inv("get_live_stats");
        document.querySelector("#earn").textContent = s.earnings_eur.toFixed(4) + " EUR";
        document.querySelector("#load").textContent = s.gpu_load + " %";
        document.querySelector("#temp").textContent = s.gpu_temp_c + " C";
        const mine = document.querySelector("#mine");
        mine.textContent = s.should_mine ? "ACTIF" : "PAUSE";
        mine.className = "cs-stat-value " + (s.should_mine ? "on" : "off");
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
