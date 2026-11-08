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
 :root{
  --bg1:#0b1224; --bg2:#0d1730; --card:#141c33; --card2:#182242;
  --line:#243056; --txt:#e6ebf5; --muted:#8b98b8;
  --acc:#818cf8; --cyan:#22d3ee; --green:#34d399; --amber:#fbbf24; --red:#f87171;
 }
 *{box-sizing:border-box}
 body{
  font-family:"Segoe UI Variable","Segoe UI",system-ui,sans-serif;
  background:radial-gradient(1200px 700px at 85% -10%, #1b2a55 0%, transparent 55%),
             linear-gradient(160deg,var(--bg1),var(--bg2) 60%,#0e1a38);
  background-attachment:fixed;color:var(--txt);margin:0;padding:0 0 60px;min-height:100vh;
 }
 .wrap{max-width:920px;margin:0 auto;padding:26px 20px}
 /* entete */
 .top{display:flex;align-items:center;justify-content:space-between;gap:14px;flex-wrap:wrap;margin-bottom:18px}
 .brand{display:flex;align-items:center;gap:12px}
 .logo{width:38px;height:38px;border-radius:12px;display:flex;align-items:center;justify-content:center;font-size:20px;
  background:linear-gradient(135deg,#22d3ee55,var(--acc));border:1px solid #2c3c6e;box-shadow:0 6px 18px #818cf833}
 h1{font-size:21px;margin:0}
 .sub{font-size:12px;color:var(--muted);letter-spacing:.05em}
 /* cartes */
 h2{font-size:13px;margin:0 0 4px;color:var(--cyan);text-transform:uppercase;letter-spacing:.1em}
 .card{background:linear-gradient(180deg,var(--card2),var(--card));border:1px solid var(--line);
  border-radius:16px;padding:18px 20px;margin-top:14px;box-shadow:0 8px 24px rgba(2,6,20,.35)}
 .card:hover{border-color:#2c3c6e}
 label{display:block;margin-top:12px;font-size:12px;color:var(--muted);text-transform:uppercase;letter-spacing:.06em}
 input,select{width:100%;padding:10px 12px;border-radius:10px;border:1px solid var(--line);
  background:#0d1428;color:var(--txt);outline:none;transition:border-color .15s, box-shadow .15s}
 input:focus,select:focus{border-color:var(--acc);box-shadow:0 0 0 3px #818cf833}
 button{margin-top:14px;padding:10px 20px;border:0;border-radius:10px;cursor:pointer;font-weight:600;font-size:14px;
  background:linear-gradient(135deg,var(--acc),#6366f1);color:#fff;box-shadow:0 6px 16px #6366f144;transition:transform .1s, box-shadow .15s}
 button:hover{transform:translateY(-1px);box-shadow:0 8px 20px #6366f166}
 button:active{transform:translateY(0)}
 button.gray{background:#1a2444;color:var(--txt);border:1px solid var(--line);box-shadow:none;padding:8px 14px;font-size:13px}
 button.blue{background:linear-gradient(135deg,#06b6d4,#0891b2);color:#03212b}
 button.danger{background:linear-gradient(135deg,#ef4444,#b91c1c);color:#fff}
 table{width:100%;border-collapse:collapse;font-size:14px;margin-top:10px}
 td,th{padding:8px 10px;border-bottom:1px solid var(--line);text-align:left}
 th{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.06em;font-weight:600}
 td{font-variant-numeric:tabular-nums}
 .ok{color:var(--green)}.err{color:var(--red)}.muted{color:var(--muted)}
 .rolebtn{display:inline-block;padding:8px 16px;border:1px solid var(--line);border-radius:10px;margin-right:8px;cursor:pointer;font-size:14px}
 .rolebtn.sel{background:linear-gradient(135deg,var(--acc),#6366f1);color:#fff;font-weight:600;border-color:transparent}
 code{background:#0d1428;border:1px solid var(--line);padding:2px 6px;border-radius:6px;font-size:13px;word-break:break-all}
 a{color:var(--cyan);text-decoration:none}
 a:hover{text-decoration:underline}
 /* tuiles stats */
 .stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:12px}
 .tile{background:linear-gradient(180deg,var(--card2),var(--card));border:1px solid var(--line);border-radius:16px;padding:14px 16px}
 .tile-label{font-size:11px;color:var(--muted);text-transform:uppercase;letter-spacing:.08em}
 .tile-value{font-size:22px;font-weight:700;margin-top:6px;font-variant-numeric:tabular-nums}
 .tile-value.money{color:var(--green)}
 /* badges statut */
 .chip{display:inline-block;padding:3px 10px;border-radius:99px;font-size:12px;font-weight:600;letter-spacing:.02em}
 .chip.run{background:#22d3ee22;color:var(--cyan);border:1px solid #22d3ee55}
 .chip.done{background:#34d39922;color:var(--green);border:1px solid #34d39955}
 .chip.fail{background:#f8717122;color:var(--red);border:1px solid #f8717155}
 /* barre de progression jobs */
 .pbar{background:#0d1428;border:1px solid var(--line);border-radius:99px;height:10px;overflow:hidden;margin-top:8px}
 .pbar>div{background:linear-gradient(90deg,var(--acc),var(--cyan));height:100%;width:0;transition:width .5s;border-radius:99px}
 .pbar.full>div{background:linear-gradient(90deg,#10b981,var(--green))}
 .job{border:1px solid var(--line);border-radius:14px;padding:14px 16px;margin-top:12px;background:#0d142855}
 .job-head{display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap}
 .job-title{font-weight:700;font-size:15px}
 .job-actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:10px}
 .job-actions button{margin-top:0}
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

// Sprint 15c : carte de gestion de compte (RGPD).
function renderAccountCard(){
 var u=user();
 // Sprint 31 : preferences de notifications e-mail (clients).
 var ntf='';
 if(u.role==='client'){
  ntf='<h2 style="margin-top:28px">Notifications par e-mail</h2>'+
  '<label style="display:block;margin:6px 0"><input type="checkbox" id="ntf-job"'+(u.notify_job_done===false?'':' checked')+'> Prévenir quand un job est terminé</label>'+
  '<label style="display:block;margin:6px 0"><input type="checkbox" id="ntf-low"'+(u.notify_low_credits===false?'':' checked')+'> Prévenir quand mes crédits sont bas (moins de 5 €)</label>'+
  '<button class="blue" onclick="saveNotifications()">Enregistrer mes préférences</button>';
 }
 document.getElementById('acct').innerHTML=
  '<label>Nom ou entreprise affiché</label>'+
  '<input id="acc-name" value="'+esc(u.display_name||'')+'">'+
  '<button class="blue" onclick="saveName()">Enregistrer le nom</button>'+
  '<h2 style="margin-top:28px">Changer mon mot de passe</h2>'+
  '<label>Mot de passe actuel</label><input id="acc-pw0" type="password">'+
  '<label>Nouveau mot de passe (8 caractères min.)</label><input id="acc-pw1" type="password">'+
  '<button class="blue" onclick="savePass()">Changer le mot de passe</button>'+
  ntf+
 '<h2 style="margin-top:28px">Mon statut</h2>'+
  '<p class="muted">Compte '+(u.account_type==='societe'?'société':'particulier')+
  (u.account_type==='societe'?(u.siret_verified?' — <span class="ok">SIRET vérifié ✓</span>':' — <span class="err">SIRET en cours de vérification</span>'):'')+'</p>'+
  '<label>Raison sociale</label><input id="soc2-name" value="'+esc(u.company_name||'')+'">'+
  '<label>N° SIRET (laisser vide pour un compte particulier)</label><input id="soc2-siret" value="'+esc(u.siret||'')+'">'+
  '<label>N° TVA intracom. (optionnel)</label><input id="soc2-vat" value="'+esc(u.vat_number||'')+'">'+
  '<button class="blue" onclick="saveCompany()">Enregistrer mon statut</button>'+
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
function saveCompany(){
 var siret=document.getElementById('soc2-siret').value.replace(/\s/g,'');
 var body={company:null};
 if(siret){
  if(siret.length!==14||!/^[0-9]{14}$/.test(siret)){
   acctMsg('Le SIRET doit contenir exactement 14 chiffres.','err');return;
  }
  body.company={name:document.getElementById('soc2-name').value.trim(),
   siret:siret,vat_number:document.getElementById('soc2-vat').value.trim().toUpperCase(),address:''};
 }
 api('/auth/company',{method:'POST',body:JSON.stringify(body)}).then(function(u2){
  localStorage.setItem('cs-user',JSON.stringify(u2));
  acctMsg('✅ Statut enregistré. La vérification INSEE du SIRET est lancée.','ok');
  setTimeout(renderAccountCard,400);
 }).catch(function(e){acctMsg('Erreur : '+esc(e.message),'err');});
}
// Sprint 31 : enregistrement des preferences de notifications.
function saveNotifications(){
 var body={notify_job_done:document.getElementById('ntf-job').checked,
  notify_low_credits:document.getElementById('ntf-low').checked};
 api('/auth/notifications',{method:'POST',body:JSON.stringify(body)}).then(function(u2){
  localStorage.setItem('cs-user',JSON.stringify(u2));
  acctMsg('\u2705 Préférences de notification enregistrées.','ok');
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
<div class="wrap">

<h1>🥗 ComputeShare — Créer un compte</h1>
<p class="muted">Déjà inscrit ? <a href="/login">Se connecter</a></p>

<div class="card">
 <h2>1. Je suis...</h2>
 <span class="rolebtn sel" id="role-client" onclick="setRole('client')">🏢 J'ai besoin de puissance (entreprise)</span>
 <span class="rolebtn" id="role-seller" onclick="setRole('seller')">🖥️ Je propose ma machine (fournisseur)</span>
</div>

<div class="card">
 <h2>1bis. Mon statut</h2>
 <span class="rolebtn sel" id="acct-part" onclick="setAcct('particulier')">👤 Un particulier</span>
 <span class="rolebtn" id="acct-soc" onclick="setAcct('societe')">🏛️ Une société</span>
 <div id="soc-fields" style="display:none">
  <label>Raison sociale</label><input id="soc-name" placeholder="ACME Corp SAS">
  <label>N° SIRET (14 chiffres)</label><input id="soc-siret" placeholder="12345678901234">
  <label>N° TVA intracommunautaire (optionnel)</label><input id="soc-vat" placeholder="FR12345678901">
  <p class="muted">Le SIRET est vérifié automatiquement auprès de l'INSEE. Les sociétés vérifiées n'ont pas les plafonds des comptes particuliers.</p>
 </div>
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
var acct='particulier';
function setAcct(a){
 acct=a;
 document.getElementById('acct-part').className='rolebtn'+(a==='particulier'?' sel':'');
 document.getElementById('acct-soc').className='rolebtn'+(a==='societe'?' sel':'');
 document.getElementById('soc-fields').style.display=a==='societe'?'block':'none';
}
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
 if(acct==='societe'){
  body.company={name:document.getElementById('soc-name').value.trim(),
   siret:document.getElementById('soc-siret').value.replace(/\s/g,''),
   vat_number:document.getElementById('soc-vat').value.trim().toUpperCase(),
   address:''};
  if(body.company.siret.length!==14||!/^[0-9]{14}$/.test(body.company.siret)){
   document.getElementById('msg').innerHTML='<p class="err">Le SIRET doit contenir exactement 14 chiffres.</p>';
   return;
  }
 }
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
</div>
</body>
</html>`

const loginHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Connexion</title>` + accountsCSS + accountsJS + `</head>
<body>
<div class="wrap">
<h1>🥗 ComputeShare — Connexion</h1>
<p class="muted">Pas encore de compte ? <a href="/signup">S'inscrire</a></p>

<div class="card">
 <label>Adresse e-mail</label><input id="email" type="email">
 <label>Mot de passe</label><input id="password" type="password">
 <button onclick="doLogin()">Se connecter</button>
 <div id="msg"></div>
 <p class="muted" style="margin-top:14px"><a href="/forgot">Mot de passe oublie ?</a></p>
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
</div>
</body>
</html>`

// GET /signup et /login.
func signupPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(signupHTML))
}

// Sprint 29 : page "mot de passe oublie".
const forgotHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Mot de passe oublie</title>` + accountsCSS + accountsJS + `</head>
<body>
<div class="wrap">
<h1>🥗 ComputeShare - Mot de passe oublie</h1>
<p class="muted"><a href="/login">Retour a la connexion</a></p>

<div class="card">
 <p>Indiquez votre adresse e-mail : nous vous enverrons un lien pour choisir un nouveau mot de passe (valable 1 heure).</p>
 <label>Adresse e-mail</label><input id="email" type="email">
 <button onclick="doForgot()">Envoyer le lien</button>
 <div id="msg"></div>
</div>

<script>
function doForgot(){
 var email=document.getElementById('email').value.trim();
 if(!email){document.getElementById('msg').innerHTML='<p class="err">Entrez votre adresse e-mail.</p>';return;}
 document.getElementById('msg').textContent='Envoi en cours...';
 fetch('/auth/forgot_password',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({email:email})})
 .then(function(r){
  if(!r.ok){return r.text().then(function(t){throw new Error(t||('HTTP '+r.status));});}
  document.getElementById('msg').innerHTML='<p class="ok">Si un compte existe pour cette adresse, un e-mail de reinitialisation vient d etre envoye (verifiez aussi votre dossier spam).</p>';
 })
 .catch(function(e){document.getElementById('msg').innerHTML='<p class="err">Erreur : '+esc(e.message)+'</p>';});
}
</script>
</div>
</body>
</html>`

// Sprint 29 : page de choix du nouveau mot de passe (lien e-mail).
const resetHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Nouveau mot de passe</title>` + accountsCSS + accountsJS + `</head>
<body>
<div class="wrap">
<h1>🥗 ComputeShare - Nouveau mot de passe</h1>
<p class="muted"><a href="/login">Retour a la connexion</a></p>

<div class="card">
 <p>Choisissez votre nouveau mot de passe (8 caracteres minimum). Toutes vos sessions ouvertes seront deconnectees.</p>
 <label>Nouveau mot de passe</label><input id="p1" type="password">
 <label>Confirmez le mot de passe</label><input id="p2" type="password">
 <button onclick="doReset()">Modifier mon mot de passe</button>
 <div id="msg"></div>
</div>

<script>
var tok=new URLSearchParams(location.search).get('token')||'';
if(!tok){document.getElementById('msg').innerHTML='<p class="err">Lien invalide : utilisez le lien recu par e-mail.</p>';}
function doReset(){
 var p1=document.getElementById('p1').value,p2=document.getElementById('p2').value;
 if(!tok){document.getElementById('msg').innerHTML='<p class="err">Lien invalide : utilisez le lien recu par e-mail.</p>';return;}
 if(p1!==p2){document.getElementById('msg').innerHTML='<p class="err">Les deux mots de passe ne correspondent pas.</p>';return;}
 if(p1.length<8){document.getElementById('msg').innerHTML='<p class="err">Mot de passe : 8 caracteres minimum.</p>';return;}
 document.getElementById('msg').textContent='Verification...';
 fetch('/auth/reset_password',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:tok,new_password:p1})})
 .then(function(r){
  if(!r.ok){return r.text().then(function(t){var j;try{j=JSON.parse(t);}catch(e){j=null;}throw new Error((j&&j.error)||t||('HTTP '+r.status));});}
  document.getElementById('msg').innerHTML='<p class="ok">Mot de passe modifie.</p><p><a href="/login">Se connecter</a></p>';
 })
 .catch(function(e){document.getElementById('msg').innerHTML='<p class="err">Erreur : '+esc(e.message)+'</p>';});
}
</script>
</div>
</body>
</html>`

func forgotPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(forgotHTML))
}

func resetPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(resetHTML))
}

func loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(loginHTML))
}

const meHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Mon espace entreprise</title>` + accountsCSS + accountsJS + `</head>
<body>
<div class="wrap">

<div class="top">
 <div class="brand">
  <div class="logo">🥗</div>
  <div><h1>ComputeShare</h1><div class="sub">ESPACE ENTREPRISE</div></div>
 </div>
 <div class="muted"><span id="who"></span> — <a href="#" onclick="logout();return false;">Se déconnecter</a></div>
</div>

<div class="stats">
 <div class="tile"><div class="tile-label">Crédits disponibles</div><div class="tile-value money" id="bal">…</div></div>
 <div class="tile"><div class="tile-label">Dépensé HT (total)</div><div class="tile-value" id="spent">…</div></div>
 <div class="tile"><div class="tile-label">Économie vs AWS</div><div class="tile-value" style="color:var(--cyan)" id="sav">…</div></div>
</div>

<div id="verifyBanner" class="card" style="display:none;border-color:#f59e0b">
 <h2>Verification de votre adresse e-mail requise</h2>
 <p class="muted">Pour soumettre des jobs, cliquez sur le lien envoye a <b id="verifyEmail"></b> (valable 1 heure). Pensez a verifier votre dossier spam.</p>
 <button class="gray" onclick="resendVerify()">Renvoyer l e-mail de verification</button>
 <span id="verifyMsg" style="margin-left:10px"></span>
</div>

<div class="card">
 <h2>Soumettre un job</h2>
 <label>Type de job</label>
 <select id="rt" onchange="rtChange()" style="width:100%;padding:8px;border-radius:8px;border:1px solid #334155;background:#0f172a;color:#e2e8f0">
   <option value="allowlist">Classique (commande autorisée, ex: ffmpeg.exe)</option>
   <option value="container">Conteneur (image Docker, ex: alpine:latest)</option>
 </select>
 <div id="imgRow" style="display:none">
  <label>Image du conteneur (registre Docker)</label><input id="img" value="alpine:latest">
 </div>
 <label>ID du job (unique)</label><input id="jid" value="" autocomplete="off" name="job-id">
 <label>Commande (ex: ffmpeg.exe, ou sh pour un conteneur)</label><input id="cmd" value="ffmpeg.exe">
 <label>Arguments (séparés par des espaces)</label><input id="args" value="">
 <label>Nombre de tâches</label><input id="count" value="1">
 <label>Nom du fichier résultat (ex: out.mp4)</label><input id="outname" value="out.mp4">
 <label>Délai maximum par tâche (secondes)</label><input id="tmo" value="600">
 <div id="assetRow" style="display:none">
  <h2 style="margin-top:18px">Fichiers d'entrée (optionnel)</h2>
  <p class="muted">Téléversez vos fichiers ici : ils seront placés dans /work du conteneur sous leur nom exact (100 Mo max par fichier). Ils sont supprimés du PC du fournisseur à la fin du job.</p>
  <input type="file" id="assetfile" style="color:#e2e8f0">
  <button class="gray" onclick="uploadAsset()">Téléverser</button>
  <div id="assetmsg"></div>
  <div id="assetlist"></div>
 </div>
 <button onclick="submitJob()">Soumettre</button>
 <div id="msg"></div>
</div>

<div class="card">
 <h2>Mes jobs</h2>
 <p class="muted">Actualisation automatique toutes les 5 secondes.</p>
 <div id="jobs">Chargement...</div>
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
// Sprint 27b : la liste des assets DOIT exister avant le premier
// renderAssets() (lecon vue en vrai : initialisee apres, le script
// s'interrompait et mesAssets restait indefinie -> "Cannot read
// properties of undefined (reading 'push')" au televersement).
var mesAssets=[];
renderAssets();
refreshBalance();refreshUsage();
var meUser=user();
if(meUser && !meUser.email_verified){
 document.getElementById('verifyBanner').style.display='';
 document.getElementById('verifyEmail').textContent=meUser.email;
}
function resendVerify(){
 document.getElementById('verifyMsg').textContent='Envoi en cours...';
 api('/auth/resend_verification',{method:'POST'}).then(function(){
  document.getElementById('verifyMsg').textContent='E-mail envoye (valable 1 heure).';
 }).catch(function(e){document.getElementById('verifyMsg').textContent='Erreur : '+esc(e.message);});
}
function refreshBalance(){
 api('/v1/balance').then(function(b){
  document.getElementById('bal').textContent=b.balance_eur.toFixed(2)+' €';
 }).catch(function(e){document.getElementById('bal').textContent='—';});
}
function refreshUsage(){
 api('/v1/usage').then(function(u){
  document.getElementById('spent').textContent=(u.spent_eur||0).toFixed(4)+' €';
  document.getElementById('sav').textContent=(u.savings_pct||0).toFixed(1)+' %';
  var h='<table>'
   +'<tr><th>Dépense HT totale</th><td>'+(u.spent_eur||0).toFixed(6)+' EUR</td></tr>'
   +'<tr><th>TVA totale</th><td>'+(u.vat_eur||0).toFixed(6)+' EUR</td></tr>'
   +'<tr><th>GPU-heures consommées</th><td>'+(u.gpu_hours||0).toFixed(4)+'</td></tr>'
   +'<tr><th>Équivalent AWS</th><td>'+(u.aws_equivalent_eur||0).toFixed(4)+' EUR</td></tr>'
   +'<tr><th>Économie vs AWS</th><td class="ok">'+(u.savings_pct||0).toFixed(1)+' %</td></tr>'
   +'<tr><th>Rechargements</th><td>'+(u.topups||0)+'</td></tr>'
   +'</table>';
  document.getElementById('usage').innerHTML=h;
 }).catch(function(e){document.getElementById('usage').innerHTML='<p class="muted">'+esc(e.message)+'</p>';});
}
function rtChange(){
 var c=document.getElementById('rt').value==='container';
 document.getElementById('imgRow').style.display=c?'block':'none';
 document.getElementById('assetRow').style.display=c?'block':'none';
 document.getElementById('cmd').value=c?'sh':'ffmpeg.exe';
}
// Sprint 27b : upload d'assets d'entree depuis la console (avant
// l'API, c'etait reserve a curl). Le fichier est envoye brut dans le
// corps de la requete, le nom passe en parametre d'URL.
function uploadAsset(){
 var f=document.getElementById('assetfile').files[0];
 if(!f){document.getElementById('assetmsg').innerHTML='<p class="err">Choisissez d\'abord un fichier.</p>';return;}
 if(f.size===0){document.getElementById('assetmsg').innerHTML='<p class="err">Le fichier est vide (0 octet) : ouvrez-le, écrivez du contenu, enregistrez (Ctrl+S), puis retéléversez-le.</p>';return;}
 if(f.size>100*1024*1024){document.getElementById('assetmsg').innerHTML='<p class="err">Fichier trop volumineux (100 Mo max).</p>';return;}
 document.getElementById('assetmsg').innerHTML='<p class="muted">Téléversement en cours...</p>';
 fetch('/v1/assets?filename='+encodeURIComponent(f.name),{
  method:'POST',
  headers:{'Authorization':'Bearer '+sess(),'Content-Type':'application/octet-stream'},
  body:f
 }).then(function(r){return r.text().then(function(t){
  var j;try{j=JSON.parse(t);}catch(e){j=null;}
  if(!r.ok){throw new Error((j&&j.error)||t||('HTTP '+r.status));}
  return j;});
 }).then(function(a){
  mesAssets.push({asset_id:a.asset_id,filename:f.name,size_bytes:a.size_bytes});
  document.getElementById('assetfile').value='';
  document.getElementById('assetmsg').innerHTML='<p class="ok">✓ '+esc(f.name)+' téléversé ('+a.size_bytes+' octets).</p>';
  renderAssets();
 }).catch(function(e){document.getElementById('assetmsg').innerHTML='<p class="err">Erreur : '+esc(e.message)+'</p>';});
}
function renderAssets(){
 var h;
 if(!mesAssets.length){h='<p class="muted">Aucun fichier joint. Les fichiers téléversés seront attachés à votre prochain job conteneur.</p>';}
 else{
  h='<table><tr><th>Fichier</th><th>Taille</th><th></th></tr>';
  mesAssets.forEach(function(a,i){
   h+='<tr><td>'+esc(a.filename)+'</td><td>'+a.size_bytes+' octets</td><td><button class="gray" onclick="removeAsset('+i+')">Retirer</button></td></tr>';
  });
  h+='</table>';
 }
 document.getElementById('assetlist').innerHTML=h;
}
function removeAsset(i){mesAssets.splice(i,1);renderAssets();}
// Sprint 27b2 : decoupe des arguments qui respecte les guillemets
// doubles - sans elle, la commande sh -c "cat entree.txt > sortie.txt"
// etait coupee en 5 morceaux et le conteneur echouait.
function splitArgs(s){
 var out=[],cur='',q=false;
 for(var i=0;i<s.length;i++){
  var c=s.charAt(i);
  if(c==='"'){q=!q;continue;}
  if(c===' '&&!q){if(cur){out.push(cur);cur='';}continue;}
  cur+=c;
 }
 if(cur)out.push(cur);
 return out;
}
function submitJob(){
 var id=document.getElementById('jid').value.trim();
 if(!id){document.getElementById('msg').innerHTML='<p class="err">Choisissez un ID de job.</p>';return;}
 var body={id:id,command:document.getElementById('cmd').value.trim(),
  args:splitArgs(document.getElementById('args').value),
  task_count:parseInt(document.getElementById('count').value)||1,
  timeout_secs:parseInt(document.getElementById('tmo').value)||600,
  output_name:document.getElementById('outname').value.trim()};
 var rt=document.getElementById('rt').value;
 if(rt==='container'){body.runtime='container';body.image=document.getElementById('img').value.trim();
  // Sprint 27b : les fichiers televerses ci-dessus sont attaches
  // au job (injection dans /work du conteneur, sprint 27).
  if(mesAssets.length){body.asset_ids=mesAssets.map(function(a){return a.asset_id;});}}
 api('/v1/jobs',{method:'POST',body:JSON.stringify(body)}).then(function(r){
  document.getElementById('msg').innerHTML='<p class="ok">Job '+esc(r.job_id)+' soumis ('+r.task_count+' tâches).</p>';
  document.getElementById('jid').value='';
  refreshJobs();
 }).catch(function(e){document.getElementById('msg').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
}
var jobsPoll=null;
function refreshJobs(){
 api('/v1/jobs').then(function(list){
  var h='';
  if(!list.length){h='<p class="muted">Aucun job pour le moment. Soumettez votre premier job ci-dessus.</p>';}
  else{
   list.slice().reverse().forEach(function(j){
    var total=j.task_count||0,ch=j.chunks||{},done=(ch.done||0)+ (ch.failed||0)+(ch.canceled||0);
    var pct=total?Math.round(done*100/total):0;
    var comp=j.status==='completed', failed=(ch.failed||0)>0;
    var badge=comp?(failed?'<span class="chip fail">terminé ('+(ch.failed||0)+' échec'+((ch.failed||0)>1?'s':'')+')</span>':'<span class="chip done">✓ terminé</span>'):'<span class="chip run">● en cours</span>';
    h+='<div class="job">'
     +'<div class="job-head"><span class="job-title">'+esc(j.job_id)+'</span>'+badge+'</div>'
     +'<div class="pbar'+(comp?' full':'')+'"><div style="width:'+pct+'%"></div></div>'
     +'<table><tr><th>Tâches</th><td>'+total+'</td></tr>';
    for(var k in ch){if(ch[k])h+='<tr><td>'+esc(k)+'</td><td>'+ch[k]+'</td></tr>';}
    if(j.cost_eur!==undefined){h+='<tr><td>Coût HT réel</td><td>'+j.cost_eur.toFixed(6)+' EUR</td></tr><tr><td>GPU-heures</td><td>'+(j.gpu_hours||0).toFixed(4)+'</td></tr>';}
    h+='</table><div class="job-actions">';
    if(!comp){
     h+='<button class="gray" onclick="cancelJob(\''+esc(j.job_id)+'\')">Annuler la file</button>';
    }
    h+='<button class="gray" onclick="showResults(\''+esc(j.job_id)+'\')">Résultats</button></div>'
     +'<div id="res-'+esc(j.job_id)+'" class="muted" style="font-size:13px;margin-top:6px"></div></div>';
   });
  }
  document.getElementById('jobs').innerHTML=h;
  refreshBalance();
 }).catch(function(e){document.getElementById('jobs').innerHTML='<p class="err">'+esc(e.message)+'</p>';});
}
function cancelJob(id){
 api('/v1/jobs/'+encodeURIComponent(id)+'/cancel',{method:'POST'}).then(function(r){
  refreshJobs();
 }).catch(function(e){alert('Annulation impossible : '+e.message);});
}
function showResults(id){
 api('/v1/jobs/'+encodeURIComponent(id)+'/results').then(function(chunks){
  var h='<table>';
  (chunks||[]).forEach(function(c){
   h+='<tr><td>'+esc(c.id)+'</td><td class="'+(c.status==='done'?'ok':'err')+'">'+esc(c.status)+'</td><td style="font-size:12px;word-break:break-all">'+esc(c.result_sha256||'')+'</td></tr>';
  });
  document.getElementById('res-'+id).innerHTML=h+'</table>';
 }).catch(function(e){document.getElementById('res-'+id).textContent=e.message;});
}
if(jobsPoll)clearInterval(jobsPoll);
refreshJobs();
jobsPoll=setInterval(refreshJobs,5000);
</script>
</div>
</body>
</html>`

const sellerHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Mon espace fournisseur</title>` + accountsCSS + accountsJS + `</head>
<body>
<div class="wrap">
<h1>🥗 Mon espace fournisseur</h1>
<p class="muted"><span id="who"></span> — <a href="#" onclick="logout();return false;">Se déconnecter</a></p>

<div id="verifyBanner" class="card" style="display:none;border-color:#f59e0b">
 <h2>Verifiez votre adresse e-mail</h2>
 <p class="muted">Un lien de verification a ete envoye a <b id="verifyEmail"></b> (valable 1 heure). Un compte non verifie est limite.</p>
 <button class="gray" onclick="resendVerify()">Renvoyer l e-mail de verification</button>
 <span id="verifyMsg" style="margin-left:10px"></span>
</div>

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
var meUser=user();
if(meUser && !meUser.email_verified){
 document.getElementById('verifyBanner').style.display='';
 document.getElementById('verifyEmail').textContent=meUser.email;
}
function resendVerify(){
 document.getElementById('verifyMsg').textContent='Envoi en cours...';
 api('/auth/resend_verification',{method:'POST'}).then(function(){
  document.getElementById('verifyMsg').textContent='E-mail envoye (valable 1 heure).';
 }).catch(function(e){document.getElementById('verifyMsg').textContent='Erreur : '+esc(e.message);});
}
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
</div>
</body>
</html>`

const adminHTML = `<!DOCTYPE html>
<html lang="fr">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>ComputeShare - Console admin</title>` + accountsCSS + `</head>
<body>
<div class="wrap">
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
</div>
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
<div class="wrap">
<h1>🥗 ComputeShare — CGU (bêta) et politique de confidentialité</h1>

<div class="card">
 <h2>Conditions générales d'utilisation (bêta)</h2>
 <p class="muted">Version du 4 novembre 2026 — service en phase bêta de test.</p>
 <p><b>1. Objet.</b> ComputeShare met en relation des entreprises qui ont besoin de puissance de calcul et des fournisseurs qui proposent la puissance de leurs machines (GPU).</p>
 <p><b>2. Compte.</b> Vous êtes responsable de la confidentialité de vos identifiants et de vos jetons. Vous devez avoir 18 ans ou l'autorisation d'un représentant légal.</p>
 <p><b>3. Service bêta.</b> Le service est fourni « en l'état », sans garantie de disponibilité. Des interruptions peuvent survenir pour maintenance ou évolution. Les données peuvent être réinitialisées pendant la phase de test.</p>
 <p><b>4. Usages interdits.</b> Contenus illégaux, atteinte aux droits d'autrui, tentative de contourner la tarification ou les contrôles anti-fraude, exécution de charges nuisibles pour les machines des fournisseurs.</p>
 <p><b>5. Tarifs et paiements.</b> Comptes particuliers : solde de crédits limité à 50 EUR et retraits limités (100 EUR / 30 jours, 500 EUR cumulés) ; ces plafonds ne s'appliquent pas aux sociétés dont le SIRET a été vérifié auprès de l'INSEE. Les entreprises achètent des crédits (euros HT) consommés à l'usage. Les gains des fournisseurs sont crédités sur leur portefeuille interne ; les retraits passent par la console d'administration après vérification. Pendant la bêta, les modalités de retrait restent à la discrétion de l'éditeur.</p>
 <p><b>6. Responsabilité.</b> L'éditeur ne saurait être tenu responsable des pertes de données, résultats de calcul ou incidents liés à la phase bêta. Sauvegardez vos données et vérifiez vos résultats.</p>
 <p><b>7. Résiliation.</b> Vous pouvez supprimer votre compte à tout moment depuis votre espace (« Mon compte »). L'éditeur peut suspendre un compte en cas de violation des présentes CGU.</p>
</div>

<div class="card">
 <h2>Politique de confidentialité (RGPD)</h2>
 <p><b>Responsable de traitement :</b> ComputeShare (contact : support@computeshare.fr).</p>
 <p><b>Données collectées</b> — comptes : adresse e-mail, statut (particulier ou société), nom affiché (facultatif), mot de passe (haché, jamais en clair), horodatage de votre consentement. Sociétés : raison sociale, SIRET (vérifié auprès de l'INSEE), numéro de TVA intracommunautaire (facultatif). Machines : identifiant, modèle de GPU, disponibilité, gains. Facturation : historique des crédits et consommations.</p>
 <p><b>Finalités :</b> fournir le service (authentification, mise en relation, facturation), prévenir la fraude, respecter les obligations comptables.</p>
 <p><b>Base légale :</b> votre consentement (inscription), l'exécution du contrat (fonctionnement du service), l'obligation légale (conservation des écritures comptables).</p>
 <p><b>Durées :</b> comptes actifs : le temps du service. Écritures comptables : durée légale de conservation. Comptes supprimés : données personnelles effacées immédiatement, seules des écritures anonymisées sont conservées.</p>
 <p><b>Vos droits :</b> accès, rectification, export (bouton « Exporter mes données »), effacement (« Supprimer mon compte »). Pour toute demande : support@computeshare.fr.</p>
 <p><b>Sous-traitants / hébergement :</b> le service est hébergé dans l'Union européenne (France). Aucune donnée n'est vendue. Aucun cookie publicitaire : seule une session locale (votre navigateur) est utilisée pour garder la connexion.</p>
 <p><b>Sécurité :</b> mots de passe hachés (bcrypt), sessions signées et révocables, jetons de nœud rotatifs, restriction des tentatives de connexion.</p>
 <p><a href="/signup">← Retour à l'inscription</a></p>
</div>
</div>
</body>
</html>`

func privacyPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(privacyHTML))
}
