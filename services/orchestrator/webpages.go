package main

// Sprint 15 (LAI-58) + 15c (LAI-60) - Pages web des comptes :
//   /signup          : inscription entreprise ou fournisseur (+ consentement)
//   /login           : connexion
//   /me              : espace entreprise (profil, credits, jobs, compte)
//   /seller          : espace fournisseur (jeton, noeuds, gains, compte)
//   /privacy         : CGU beta + politique de confidentialite (RGPD)
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

// Sprint 15c : carte de gestion de compte (RGPD).
function renderAccountCard(){
 var u=user();
 document.getElementById('acct').innerHTML=
  '<label>Nom ou entreprise affiché</label>'+
  '<input id="acc-name" value="'+esc(u.display_name||'')+'">'+
  '<button class="blue" onclick="saveName()">Enregistrer le nom</button>'+
  '<h2 style="margin-top:28px">Changer mon mot de passe</h2>'+
  '<label>Mot de passe actuel</label><input id="acc-pw0" type="password">'+
  '<label>Nouveau mot de passe (8 caractères min.)</label><input id="acc-pw1" type="password">'+
  '<button class="blue" onclick="savePass()">Changer le mot de passe</button>'+
  '<h2 style="margin-top:28px">Mes données (RGPD)</h2>'+
  '<button class="gray" onclick="doExport()">⬇️ Exporter mes données (JSON)</button>'+
  '<p class="muted">La suppression du compte est définitive : nom, e-mail et mot de passe sont effacés'+(u.role==='seller'?', vos machines sont détachées':'')+'. Cette action ne peut pas être annulée.</p>'+
  '<button style="background:#ef4444;color:#fff" onclick="deleteAccount()">🗑️ Supprimer mon compte</button>'+
  '<div id="acct-msg" style="margin-top:10px"></div>';
}
function acctMsg(t,cls){document.getElementById('acct-msg').innerHTML='<p class="'+(cls||'')+'">'+t+'</p>';}
function saveName(){
 var v=document.getElementById('acc-name').value.trim();
 if(!v){acctMsg('Le nom ne peut pas être vide.','err');return;}
 api('/auth/profile',{method:'POST',body:JSON.stringify({name:v})}).then(function(u){
  localStorage.setItem('cs-user',JSON.stringify(u));
  acctMsg('✅ Nom enregistré.','ok');
 }).catch(function(e){acctMsg('Erreur : '+esc(e.message),'err');});
}
function savePass(){
 var p0=document.getElementById('acc-pw0').value,p1=document.getElementById('acc-pw1').value;
 if(p1.length<8){acctMsg('Le nouveau mot de passe doit faire 8 caractères minimum.','err');return;}
 api('/auth/password',{method:'POST',body:JSON.stringify({current_password:p0,new_password:p1})}).then(function(r){
  localStorage.setItem('cs-session',r.session_token);
  acctMsg('✅ Mot de passe changé. Vos autres appareils ont été déconnectés.','ok');
  document.getElementById('acc-pw0').value='';document.getElementById('acc-pw1').value='';
 }).catch(function(e){acctMsg('Erreur : '+esc(e.message),'err');});
}
function doExport(){
 api('/auth/export').then(function(j){
  var b=new Blob([JSON.stringify(j,null,2)],{type:'application/json'});
  var a=document.createElement('a');a.href=URL.createObjectURL(b);
  a.download='computeshare-export.json';a.click();URL.revokeObjectURL(a.href);
 }).catch(function(e){acctMsg('Erreur : '+esc(e.message),'err');});
}
function deleteAccount(){
 var p=window.prompt('Suppression définitive du compte.\nEntrez votre mot de passe pour confirmer :');
 if(!p){return;}
 if(!window.confirm('Dernière confirmation : supprimer définitivement ce compte et toutes ses données personnelles ?')){return;}
 api('/auth/account',{method:'DELETE',body:JSON.stringify({password:p})}).then(function(){
  logout();
 }).catch(function(e){acctMsg('Erreur : '+esc(e.message),'err');});
}
</script>
`

const signupHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Inscription</title>` + accountsCSS + accountsJS + `</head>
<body>
<h1>🥗 ComputeShare — Créer un compte</h1>
<p class="muted">Déjà inscrit ? <a href="/login">Se connecter</a></p>

<div class="card">
 <h2>1. Je suis...</h2>
 <span class="rolebtn sel" id="role-client" onclick="setRole('client')">🏢 J'ai besoin de puissance (entreprise)</span>
 <span class="rolebtn" id="role-seller" onclick="setRole('seller')">🖥️ Je propose ma machine (fournisseur)</span>
</div>

<div class="card">
 <h2>2. Mes informations</h2>
 <label>Nom ou entreprise (optionnel)</label><input id="name" placeholder="Jean Dupont / ACME Corp">
 <label>Adresse e-mail</label><input id="email" type="email" placeholder="vous@exemple.fr">
 <label>Mot de passe (8 caractères minimum)</label><input id="password" type="password">
 <label style="margin-top:14px;font-size:14px;color:#e2e8f0"><input type="checkbox" id="consent" style="width:auto;margin-right:6px">J'accepte les <a href="/privacy" target="_blank">CGU et la politique de confidentialité</a> (obligatoire)</label>
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
 if(!document.getElementById('consent').checked){
  document.getElementById('msg').innerHTML='<p class="err">Vous devez accepter les CGU et la politique de confidentialité pour créer un compte.</p>';
  return;
 }
 var body={email:document.getElementById('email').value.trim(),
  password:document.getElementById('password').value,
  name:document.getElementById('name').value.trim(),role:role,consent:true};
 fetch('/auth/signup',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
 .then(function(r){return r.text().then(function(t){var j;try{j=JSON.parse(t);}catch(e){j=null;}
   if(!r.ok){throw new Error((j&&j.error)||t||('HTTP '+r.status));}return j;});})
 .then(function(resp){
  saveAuth(resp);
  if(role==='seller'){
   document.getElementById('msg').innerHTML='<div class="card"><h2>✅ Compte fournisseur créé !</h2>'+
    '<p>Votre <b>jeton de nœud personnel</b> (à coller dans l\'application desktop) :</p>'+
    '<p><code>'+resp.node_token+'</code></p>'+
    '<button onclick="location.href=\'/seller\'">Continuer vers mon espace fournisseur</button></div>';
  }else{
   document.getElementById('msg').innerHTML='<div class="card"><h2>✅ Compte entreprise créé !</h2>'+
    '<p>Votre clé API B2B : <code>'+resp.api_key+'</code> (notez-la, elle ne sera plus affichée).</p>'+
    '<button onclick="location.href=\'/me\'">Continuer vers mon espace entreprise</button></div>';
  }
 })
 .catch(function(e){document.getElementById('msg').innerHTML='<p class="err">Erreur : '+esc(e.message)+'</p>';});
}
</script>
</body>
</html>`

const loginHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Connexion</title>` + accountsCSS + accountsJS + `</head>
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
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Mon espace entreprise</title>` + accountsCSS + accountsJS + `</head>
<body>
<h1>🥗 Mon espace entreprise</h1>
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

<div class="card">
 <h2>Mon compte</h2>
 <div id="acct"></div>
</div>

<script>
if(!requireRole('client'))throw new Error('redirection');
document.getElementById('who').textContent=user().email;
renderAccountCard();
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
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Mon espace fournisseur</title>` + accountsCSS + accountsJS + `</head>
<body>
<h1>🥗 Mon espace fournisseur</h1>
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

<div class="card">
 <h2>Mon compte</h2>
 <div id="acct"></div>
</div>

<script>
if(!requireRole('seller'))throw new Error('redirection');
document.getElementById('who').textContent=user().email;
renderAccountCard();
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
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Console admin</title>` + accountsCSS + `</head>
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

// GET /privacy : CGU beta + politique de confidentialite (Sprint 15c,
// RGPD). Page statique, lipee depuis l'inscription.
const privacyHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - CGU et confidentialité</title>` + accountsCSS + `</head>
<body>
<h1>🥗 ComputeShare — CGU (bêta) et politique de confidentialité</h1>

<div class="card">
 <h2>Conditions générales d'utilisation (bêta)</h2>
 <p class="muted">Version du 4 novembre 2026 — service en phase bêta de test.</p>
 <p><b>1. Objet.</b> ComputeShare met en relation des entreprises qui ont besoin de puissance de calcul et des fournisseurs qui proposent la puissance de leurs machines (GPU).</p>
 <p><b>2. Compte.</b> Vous êtes responsable de la confidentialité de vos identifiants et de vos jetons. Vous devez avoir 18 ans ou l'autorisation d'un représentant légal.</p>
 <p><b>3. Service bêta.</b> Le service est fourni « en l'état », sans garantie de disponibilité. Des interruptions peuvent survenir pour maintenance ou évolution. Les données peuvent être réinitialisées pendant la phase de test.</p>
 <p><b>4. Usages interdits.</b> Contenus illégaux, atteinte aux droits d'autrui, tentative de contourner la tarification ou les contrôles anti-fraude, exécution de charges nuisibles pour les machines des fournisseurs.</p>
 <p><b>5. Tarifs et paiements.</b> Les entreprises achètent des crédits (euros HT) consommés à l'usage. Les gains des fournisseurs sont crédités sur leur portefeuille interne ; les retraits passent par la console d'administration après vérification. Pendant la bêta, les modalités de retrait restent à la discrétion de l'éditeur.</p>
 <p><b>6. Responsabilité.</b> L'éditeur ne saurait être tenu responsable des pertes de données, résultats de calcul ou incidents liés à la phase bêta. Sauvegardez vos données et vérifiez vos résultats.</p>
 <p><b>7. Résiliation.</b> Vous pouvez supprimer votre compte à tout moment depuis votre espace (« Mon compte »). L'éditeur peut suspendre un compte en cas de violation des présentes CGU.</p>
</div>

<div class="card">
 <h2>Politique de confidentialité (RGPD)</h2>
 <p><b>Responsable de traitement :</b> ComputeShare (contact : support@computeshare.fr).</p>
 <p><b>Données collectées</b> — comptes : adresse e-mail, nom affiché (facultatif), mot de passe (haché, jamais en clair), horodatage de votre consentement. Machines : identifiant, modèle de GPU, disponibilité, gains. Facturation : historique des crédits et consommations.</p>
 <p><b>Finalités :</b> fournir le service (authentification, mise en relation, facturation), prévenir la fraude, respecter les obligations comptables.</p>
 <p><b>Base légale :</b> votre consentement (inscription), l'exécution du contrat (fonctionnement du service), l'obligation légale (conservation des écritures comptables).</p>
 <p><b>Durées :</b> comptes actifs : le temps du service. Écritures comptables : durée légale de conservation. Comptes supprimés : données personnelles effacées immédiatement, seules des écritures anonymisées sont conservées.</p>
 <p><b>Vos droits :</b> accès, rectification, export (bouton « Exporter mes données »), effacement (« Supprimer mon compte »). Pour toute demande : support@computeshare.fr.</p>
 <p><b>Sous-traitants / hébergement :</b> le service est hébergé dans l'Union européenne (France). Aucune donnée n'est vendue. Aucun cookie publicitaire : seule une session locale (votre navigateur) est utilisée pour garder la connexion.</p>
 <p><b>Sécurité :</b> mots de passe hachés (bcrypt), sessions signées et révocables, jetons de nœud rotatifs, restriction des tentatives de connexion.</p>
 <p><a href="/signup">← Retour à l'inscription</a></p>
</div>
</body>
</html>`

func privacyPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(privacyHTML))
}
