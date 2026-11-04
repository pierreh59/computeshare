package main

// Sprint 15 (LAI-58) - Pages web des comptes, servies par l'orchestrateur :
//   /signup          : inscription client ou vendeur
//   /login           : connexion
//   /me              : espace client (profil, credits, jobs)
//   /seller          : espace vendeur (jeton, noeuds, gains)
//   /admin/console   : mini-console admin (cle X-Admin-Key)
// Style et mode d'emploi identiques a la console B2B existante :
// pages autonomes, JS natif, appels directs a l'API.

import "net/http"

// CSS commun aux pages de comptes.
const accountsCSS = `
<style>
 body{font-family:Segoe UI,system-ui,sans-serif;max-width:860px;margin:24px auto;padding:0 16px;background:#0f172a;color:#e2e8f0}
 h1{font-size:22px} h2{font-size:16px;margin-top:28px;color:#7dd3fc}
 .card{background:#1e293b;border-radius:12px;padding:16px;margin-top:12px}
 label{display:block;margin-top:10px;font-size:13px;color:#94a3b8}
 input{width:100%;padding:8px;border-radius:8px;border:1px solid #334155;background:#0f172a;color:#e2e8f0;box-sizing:border-box}
 button{margin-top:14px;padding:10px 18px;border:0;border-radius:8px;background:#22c55e;color:#04220f;font-weight:600;cursor:pointer}
 button.gray{background:#475569;color:#e2e8f0}
 button.blue{background:#0ea5e9;color:#04222e}
 table{width:100%;border-collapse:collapse;font-size:14px;margin-top:8px}
 td,th{padding:6px 8px;border-bottom:1px solid #334155;text-align:left}
 .ok{color:#4ade80}.err{color:#f87171}.muted{color:#94a3b8}
 .rolebtn{display:inline-block;padding:8px 16px;border:1px solid #334155;border-radius:8px;margin-right:8px;cursor:pointer;font-size:14px}
 .rolebtn.sel{background:#0ea5e9;color:#04222e;font-weight:600;border-color:#0ea5e9}
 code{background:#0f172a;padding:2px 6px;border-radius:6px;font-size:13px;word-break:break-all}
 a{color:#7dd3fc}
</style>
`

// JS commun : appel API + helpers session.
const accountsJS = `
<script>
function sess(){return localStorage.getItem('cs-session')||'';}
function user(){try{return JSON.parse(localStorage.getItem('cs-user')||'null');}catch(e){return null;}}
function saveAuth(resp){
 localStorage.setItem('cs-session',resp.session_token);
 localStorage.setItem('cs-user',JSON.stringify(resp.user));
 if(resp.node_token){localStorage.setItem('cs-signup-token',resp.node_token);}
}
function api(path,opts){
 opts=opts||{};
 opts.headers=Object.assign({'Content-Type':'application/json','Authorization':'Bearer '+sess()},opts.headers||{});
 return fetch(path,opts).then(function(r){return r.text().then(function(t){
   var j;try{j=JSON.parse(t);}catch(e){j=null;}
   if(!r.ok){throw new Error((j&&j.error)||t||('HTTP '+r.status));}
   return j;});});
}
function requireRole(role){
 var u=user();
 if(!u||!sess()){location.href='/login';return false;}
 if(role&&u.role!==role){
   location.href=u.role==='seller'?'/seller':'/me';return false;
 }
 return true;
}
function logout(){localStorage.removeItem('cs-session');localStorage.removeItem('cs-user');location.href='/login';}
function fmtEur(v){return (Math.round(v*1000000)/1000000).toFixed(6)+' EUR';}
function esc(s){var d=document.createElement('div');d.textContent=s==null?'':String(s);return d.innerHTML;}
</script>
`

const signupHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><title>ComputeShare - Inscription</title>` + accountsCSS + accountsJS + `</head>
<body>
<h1>🥗 ComputeShare — Créer un compte</h1>
<p class="muted">Déjà inscrit ? <a href="/login">Se connecter</a></p>

<div class="card">
 <h2>1. Je suis...</h2>
 <span class="rolebtn sel" id="role-client" onclick="setRole('client')">🏢 J'ai besoin de puissance (client)</span>
 <span class="rolebtn" id="role-seller" onclick="setRole('seller')">🖥️ Je propose ma machine (vendeur)</span>
</div>

<div class="card">
 <h2>2. Mes informations</h2>
 <label>Nom ou entreprise (optionnel)</label><input id="name" placeholder="Jean Dupont / ACME Corp">
 <label>Adresse e-mail</label><input id="email" type="email" placeholder="vous@exemple.fr">
 <label>Mot de passe (8 caractères minimum)</label><input id="password" type="password">
 <button onclick="doSignup()">Créer mon compte</button>
 <div id="msg"></div>
</div>

<script>
var role='client';
function setRole(r){
 role=r;
 document.getElementById('role-client').className='rolebtn'+(r==='client'?' sel':'');
 document.getElementById('role-seller').className='rolebtn'+(r==='seller'?' sel':'');
}
function doSignup(){
 var body={email:document.getElementById('email').value.trim(),
  password:document.getElementById('password').value,
  name:document.getElementById('name').value.trim(),role:role};
 fetch('/auth/signup',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
 .then(function(r){return r.text().then(function(t){var j;try{j=JSON.parse(t);}catch(e){j=null;}
   if(!r.ok){throw new Error((j&&j.error)||t||('HTTP '+r.status));}return j;});})
 .then(function(resp){
  saveAuth(resp);
  if(role==='seller'){
   document.getElementById('msg').innerHTML='<div class="card"><h2>✅ Compte vendeur créé !</h2>'+
    '<p>Votre <b>jeton de nœud personnel</b> (à coller dans l\'application desktop) :</p>'+
    '<p><code>'+resp.node_token+'</code></p>'+
    '<button onclick="location.href=\'/seller\'">Continuer vers mon espace vendeur</button></div>';
  }else{
   document.getElementById('msg').innerHTML='<div class="card"><h2>✅ Compte client créé !</h2>'+
    '<p>Votre clé API B2B : <code>'+resp.api_key+'</code> (notez-la, elle ne sera plus affichée).</p>'+
    '<button onclick="location.href=\'/me\'">Continuer vers mon espace client</button></div>';
  }
 })
 .catch(function(e){document.getElementById('msg').innerHTML='<p class="err">Erreur : '+esc(e.message)+'</p>';});
}
</script>
</body>
</html>`

const loginHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><title>ComputeShare - Connexion</title>` + accountsCSS + accountsJS + `</head>
<body>
<h1>🥗 ComputeShare — Connexion</h1>
<p class="muted">Pas encore de compte ? <a href="/signup">S'inscrire</a></p>

<div class="card">
 <label>Adresse e-mail</label><input id="email" type="email">
 <label>Mot de passe</label><input id="password" type="password">
 <button onclick="doLogin()">Se connecter</button>
 <div id="msg"></div>
</div>

<script>
function doLogin(){
 var body={email:document.getElementById('email').value.trim(),
  password:document.getElementById('password').value};
 fetch('/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
 .then(function(r){return r.text().then(function(t){var j;try{j=JSON.parse(t);}catch(e){j=null;}
   if(!r.ok){throw new Error((j&&j.error)||t||('HTTP '+r.status));}return j;});})
 .then(function(resp){saveAuth(resp);location.href=resp.user.role==='seller'?'/seller':'/me';})
 .catch(function(e){document.getElementById('msg').innerHTML='<p class="err">Erreur : '+esc(e.message)+'</p>';});
}
</script>
</body>
</html>`

// GET /signup et /login.
func signupPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(signupHTML))
}

func loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(loginHTML))
}

const meHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><title>ComputeShare - Mon espace client</title>` + accountsCSS + accountsJS + `</head>
<body>
<h1>🥗 Mon espace client</h1>
<p class="muted"><span id="who"></span> — <a href="#" onclick="logout();return false;">Se déconnecter</a></p>

<div class="card">
 <h2>Crédits</h2>
 <div id="bal">Actualisation...</div>
</div>

<div class="card">
 <h2>Soumettre un job</h2>
 <label>ID du job (unique)</label><input id="jid" value="">
 <label>Commande (ex: ffmpeg.exe)</label><input id="cmd" value="ffmpeg.exe">
 <label>Arguments (séparés par des espaces)</label><input id="args" value="">
 <label>Nombre de tâches</label><input id="count" value="1">
 <label>Nom du fichier résultat (ex: out.mp4)</label><input id="outname" value="out.mp4">
 <button onclick="submitJob()">Soumettre</button>
 <div id="msg"></div>
</div>

<div class="card" id="trackCard" style="display:none">
 <h2>Suivi : <span id="tjid"></span></h2>
 <div class="bar" style="background:#334155;border-radius:99px;height:14px;overflow:hidden;margin-top:8px"><div id="pbar" style="background:#22c55e;height:100%;width:0;transition:width .5s"></div></div>
 <table id="stats"></table>
</div>

<div class="card">
 <h2>Mon usage</h2>
 <button class="gray" onclick="refreshUsage()">Actualiser</button>
 <div id="usage"></div>
</div>

<script>
if(!requireRole('client'))throw new Error('redirection');
document.getElementById('who').textContent=user().email;
refreshBalance();refreshUsage();
function refreshBalance(){
 api('/v1/balance').then(function(b){
  document.getElementById('bal').innerHTML='<p>Solde : <b>'+b.balance_eur.toFixed(2)+' EUR</b></p>';
 }).catch(function(e){document.getElementById('bal').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
}
function refreshUsage(){
 api('/v1/usage').then(function(u){
  var h='<table><tr><th>Mois</th><th>GPU-heures</th><th>Coût HT</th></tr>';
  (u.months||[]).forEach(function(m){h+='<tr><td>'+esc(m.month)+'</td><td>'+(m.gpu_hours||0).toFixed(4)+'</td><td>'+(m.cost_eur||0).toFixed(4)+' EUR</td></tr>';});
  document.getElementById('usage').innerHTML=h+'</table>';
 }).catch(function(e){document.getElementById('usage').innerHTML='<p class="muted">'+esc(e.message)+'</p>';});
}
function submitJob(){
 var id=document.getElementById('jid').value.trim();
 if(!id){document.getElementById('msg').innerHTML='<p class="err">Choisissez un ID de job.</p>';return;}
 var body={id:id,command:document.getElementById('cmd').value.trim(),
  args:document.getElementById('args').value.split(' ').filter(Boolean),
  task_count:parseInt(document.getElementById('count').value)||1,
  timeout_secs:600,output_name:document.getElementById('outname').value.trim()};
 api('/v1/jobs',{method:'POST',body:JSON.stringify(body)}).then(function(r){
  document.getElementById('msg').innerHTML='<p class="ok">Job '+esc(r.job_id+' soumis ('+r.task_count+' tâches)')+'</p>';
  startTracking(body.id);
 }).catch(function(e){document.getElementById('msg').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
}
var poll=null;
function startTracking(job){
 document.getElementById('trackCard').style.display='block';
 document.getElementById('tjid').textContent=job;
 if(poll)clearInterval(poll);
 poll=setInterval(function(){tick(job);},2000);tick(job);
}
function tick(job){
 api('/v1/jobs/'+encodeURIComponent(job)+'/status').then(function(st){
  var total=0;for(var k in st.chunks){total+=st.chunks[k];}
  var pct=total?Math.round((st.chunks.done||0)*100/total):0;
  document.getElementById('pbar').style.width=pct+'%';
  var rows='<tr><th>Status</th><td>'+esc(st.status)+' ('+pct+'%)</td></tr>';
  for(var k in st.chunks){rows+='<tr><td>'+esc(k)+'</td><td>'+st.chunks[k]+'</td></tr>';}
  if(st.cost_eur!==undefined){rows+='<tr><td>Coût HT réel</td><td>'+st.cost_eur.toFixed(6)+' EUR</td></tr><tr><td>Équivalent AWS</td><td>'+st.aws_equiv_eur.toFixed(6)+' EUR</td></tr>';}
  document.getElementById('stats').innerHTML=rows;
  if(st.status==='completed'){clearInterval(poll);poll=null;refreshBalance();refreshUsage();}
 }).catch(function(e){clearInterval(poll);poll=null;});
}
</script>
</body>
</html>`

const sellerHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><title>ComputeShare - Mon espace vendeur</title>` + accountsCSS + accountsJS + `</head>
<body>
<h1>🥗 Mon espace vendeur</h1>
<p class="muted"><span id="who"></span> — <a href="#" onclick="logout();return false;">Se déconnecter</a></p>

<div class="card">
 <h2>Mes gains</h2>
 <div id="wal">Actualisation...</div>
</div>

<div class="card">
 <h2>Mon jeton de nœud</h2>
 <p class="muted">Ce jeton identifie vos machines sur la plateforme. Collez-le dans l'application desktop à la place de l'ancien jeton.</p>
 <div id="tok"></div>
 <button class="gray" onclick="rotate()">Générer un nouveau jeton</button>
 <p class="muted">⚠️ Le nouveau jeton remplace immédiatement l'ancien (perte ou compromission).</p>
</div>

<div class="card">
 <h2>Mes machines</h2>
 <button class="gray" onclick="refreshNodes()">Actualiser</button>
 <div id="nodes"></div>
</div>

<script>
if(!requireRole('seller'))throw new Error('redirection');
document.getElementById('who').textContent=user().email;
refreshNodes();refreshWallet();showToken();
function refreshWallet(){
 api('/seller/wallet').then(function(w){
  var h='<p>Total des gains : <b>'+w.total_balance.toFixed(6)+' EUR</b></p>';
  if(w.nodes&&w.nodes.length){
   h+='<table><tr><th>Machine</th><th>GPU</th><th>En ligne</th><th>Gains</th></tr>';
   w.nodes.forEach(function(n){h+='<tr><td>'+esc(n.node_id)+'</td><td>'+esc(n.gpu_model)+'</td><td>'+(n.online?'<span class="ok">oui</span>':'<span class="err">non</span>')+'</td><td>'+n.balance.toFixed(6)+' EUR</td></tr>';});
   h+='</table>';
  }else{h+='<p class="muted">Aucune machine enregistrée pour le moment. Installez l\'application desktop avec votre jeton de nœud.</p>';}
  document.getElementById('wal').innerHTML=h;
 }).catch(function(e){document.getElementById('wal').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
}
function refreshNodes(){
 api('/seller/nodes').then(function(ns){
  var h;
  if(!ns||!ns.length){h='<p class="muted">Aucune machine pour le moment.</p>';}
  else{h='<table><tr><th>ID</th><th>GPU</th><th>Catégorie</th><th>Statut</th><th>Dernière activité</th></tr>';
   ns.forEach(function(n){h+='<tr><td>'+esc(n.id)+'</td><td>'+esc(n.gpu_model)+'</td><td>'+esc(n.gpu_category)+'</td><td>'+(n.online?'<span class="ok">en ligne</span>':'<span class="err">hors ligne</span>')+'</td><td>'+esc(new Date(n.last_seen).toLocaleString())+'</td></tr>';});
   h+='</table>';}
  document.getElementById('nodes').innerHTML=h;
 }).catch(function(e){document.getElementById('nodes').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
}
function showToken(){
 var t=localStorage.getItem('cs-signup-token');
 document.getElementById('tok').innerHTML=t?'<p><code>'+esc(t)+'</code></p>':'<p class="muted">Jeton masqué (affiché à l\'inscription et à chaque génération). Utilisez "Générer un nouveau jeton" pour l\'afficher à nouveau.</p>';
}
function rotate(){
 api('/auth/seller-token',{method:'POST'}).then(function(r){
  localStorage.setItem('cs-signup-token',r.node_token);
  showToken();
 }).catch(function(e){alert('Erreur : '+e.message);});
}
</script>
</body>
</html>`

const adminHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><title>ComputeShare - Console admin</title>` + accountsCSS + `</head>
<body>
<h1>🥗 Console admin</h1>
<p class="muted">Accès réservé. La clé admin est dans le fichier .env du serveur (ADMIN_KEY).</p>

<div class="card">
 <label>Clé admin</label><input id="akey" type="password">
 <button onclick="refreshAll()">Charger</button>
</div>

<div class="card"><h2>Flotte (nœuds en ligne)</h2><div id="nodes"></div></div>
<div class="card"><h2>Crédits par organisation</h2><div id="billing"></div></div>
<div class="card"><h2>Tarifs actuels</h2><div id="pricing"></div></div>
<div class="card"><h2>Alertes (churn / fraude)</h2><div id="alerts"></div></div>

<script>
function akey(){return document.getElementById('akey').value.trim();}
function adm(path){
 return fetch(path,{headers:{'X-Admin-Key':akey()}}).then(function(r){return r.text().then(function(t){
  var j;try{j=JSON.parse(t);}catch(e){j=null;}
  if(!r.ok){throw new Error(t||('HTTP '+r.status));}return j;});});
}
function esc(s){var d=document.createElement('div');d.textContent=s==null?'':String(s);return d.innerHTML;}
function refreshAll(){
 adm('/nodes').then(function(ns){
  var h='<table><tr><th>ID</th><th>GPU</th><th>Catégorie</th><th>Trust</th></tr>';
  (ns||[]).forEach(function(n){h+='<tr><td>'+esc(n.id)+'</td><td>'+esc(n.gpu_model)+'</td><td>'+esc(n.gpu_category)+'</td><td>'+(n.trust_score||0)+'</td></tr>';});
  document.getElementById('nodes').innerHTML=h+'</table>';
 }).catch(function(e){document.getElementById('nodes').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
 adm('/admin/billing').then(function(b){
  var h='<table><tr><th>Organisation</th><th>Solde</th></tr>';
  (b.balances||[]).forEach(function(o){h+='<tr><td>'+esc(o.org_id)+'</td><td>'+o.balance_eur.toFixed(2)+' EUR</td></tr>';});
  document.getElementById('billing').innerHTML=h+'</table>';
 }).catch(function(e){document.getElementById('billing').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
 adm('/admin/pricing').then(function(p){
  var h='<table><tr><th>Catégorie</th><th>Tarif horaire vendeur</th><th>Plancher</th></tr>';
  for(var c in p.current){h+='<tr><td>'+esc(c)+'</td><td>'+p.current[c].toFixed(2)+' EUR/h</td><td>'+(p.floors&&p.floors[c]!==undefined?p.floors[c]:'-')+'</td></tr>';}
  document.getElementById('pricing').innerHTML=h+'</table>';
 }).catch(function(e){document.getElementById('pricing').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
 Promise.all([adm('/admin/alerts'),adm('/admin/fraud')]).then(function(res){
  var h='<pre>churn : '+esc(JSON.stringify(res[0],null,1))+'\nfraude : '+esc(JSON.stringify(res[1],null,1))+'</pre>';
  document.getElementById('alerts').innerHTML=h;
 }).catch(function(e){document.getElementById('alerts').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
}
</script>
</body>
</html>`

// Pages servies.
func mePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(meHTML))
}

func sellerPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(sellerHTML))
}

func adminPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(adminHTML))
}
