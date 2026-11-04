package main

// LAI-31 - Console B2B : formulaire de soumission + suivi live.
// Page unique servie par l'orchestrateur sur /console. Elle consomme
// l'API v1 (cle API de l'organisation) et affiche :
//   - progression des chunks en direct (polling /status),
//   - cout reel du job (frais + TVA + GPU-heures),
//   - comparaison vs AWS equivalent et economie en %,
//   - solde de credits et journal des evenements.

import "net/http"

const consoleHTML = `<!DOCTYPE html>
<html lang="fr">
<head>
<meta charset="utf-8">
<title>ComputeShare - Console client</title>
<style>
 body{font-family:Segoe UI,system-ui,sans-serif;max-width:900px;margin:24px auto;padding:0 16px;background:#0f172a;color:#e2e8f0}
 h1{font-size:22px} h2{font-size:16px;margin-top:28px;color:#7dd3fc}
 .card{background:#1e293b;border-radius:12px;padding:16px;margin-top:12px}
 label{display:block;margin-top:10px;font-size:13px;color:#94a3b8}
 input{width:100%;padding:8px;border-radius:8px;border:1px solid #334155;background:#0f172a;color:#e2e8f0;box-sizing:border-box}
 button{margin-top:14px;padding:10px 18px;border:0;border-radius:8px;background:#22c55e;color:#04220f;font-weight:600;cursor:pointer}
 button.gray{background:#475569;color:#e2e8f0}
 table{width:100%;border-collapse:collapse;font-size:14px;margin-top:8px}
 td,th{padding:6px 8px;border-bottom:1px solid #334155;text-align:left}
 .ok{color:#4ade80}.err{color:#f87171}.muted{color:#94a3b8}
 .bar{background:#334155;border-radius:99px;height:14px;overflow:hidden;margin-top:8px}
 .bar>div{background:#22c55e;height:100%;width:0;transition:width .5s}
 #log div{font-size:13px;padding:3px 0;border-bottom:1px dashed #334155}
</style>
</head>
<body>
<h1>🥗 ComputeShare — Console client B2B</h1>

<div class="card">
 <h2>1. Identite</h2>
 <label>Cle API de l'organisation</label>
 <input id="key" placeholder="cshare_..." value="">
</div>

<div class="card">
 <h2>2. Soumettre un job</h2>
 <label>ID du job</label><input id="jid" value="job-demo-1">
 <label>Commande</label><input id="cmd" value="echo">
 <label>Arguments (separes par des espaces)</label><input id="args" value="hello">
 <label>Nombre de taches</label><input id="count" value="4">
 <label>Nom du fichier resultat (ex: out.mp4)</label><input id="outname" value="out.mp4">
 <label>Duree estimee par tache (heures GPU, pour la simulation de cout)</label><input id="hours" value="1">
 <label>Categorie GPU estimee (consumer / prosumer / high_end)</label><input id="cat" value="prosumer">
 <button onclick="estimate()">Simuler le cout</button>
 <button class="gray" onclick="submitJob()">Soumettre</button>
 <div id="est"></div>
</div>

<div class="card" id="trackCard" style="display:none">
 <h2>3. Suivi live : <span id="tjid"></span></h2>
 <div class="bar"><div id="pbar"></div></div>
 <table id="stats"></table>
 <div id="cost"></div>
</div>

<div class="card">
 <h2>4. Solde de credits</h2>
 <button class="gray" onclick="refreshBalance()">Actualiser le solde</button>
 <div id="bal"></div>
</div>

<div class="card">
 <h2>5. Journal</h2>
 <div id="log"></div>
</div>

<script>
var poll=null, prices={consumer:0.15,prosumer:0.25,high_end:0.35},
    MARGIN=1.30, VAT=0.20, AWS=1.00;
function log(m,cls){var d=document.createElement('div');d.innerHTML='<span class="muted">'+new Date().toLocaleTimeString()+'</span> '+m;d.className=cls||'';document.getElementById('log').prepend(d);}
function api(path,opts){opts=opts||{};opts.headers=Object.assign({'Content-Type':'application/json','Authorization':'Bearer '+key()},opts.headers||{});return fetch(path,opts).then(function(r){return r.text().then(function(t){var j;try{j=JSON.parse(t);}catch(e){j=null;}if(!r.ok)throw new Error((j&&j.error)||t||('HTTP '+r.status));return j;});});}
function key(){return document.getElementById('key').value.trim();}
function estimate(){
  var h=parseFloat(document.getElementById('hours').value)||0,
      c=parseInt(document.getElementById('count').value)||0,
      p=prices[document.getElementById('cat').value]||0.25,
      ours=p*MARGIN*h*c, aws=AWS*h*c;
  document.getElementById('est').innerHTML =
   '<table><tr><th></th><th>ComputeShare</th><th>AWS equivalent</th></tr>'+
   '<tr><td>Cout HT estime</td><td>'+ours.toFixed(2)+' EUR</td><td>'+aws.toFixed(2)+' EUR</td></tr>'+
   '<tr><td>TVA 20%</td><td>'+(ours*VAT).toFixed(2)+' EUR</td><td>'+(aws*VAT).toFixed(2)+' EUR</td></tr>'+
   '<tr><td>Total TTC</td><td>'+(ours*(1+VAT)).toFixed(2)+' EUR</td><td>'+(aws*(1+VAT)).toFixed(2)+' EUR</td></tr></table>'+
   '<p class="ok">Economie estimee vs AWS : '+((1-ours/aws)*100).toFixed(0)+' %</p>';
  log('Simulation : '+c+' taches x '+h+' h en '+document.getElementById('cat').value);
}
function submitJob(){
  var body={id:jid(),command:cmd_(),args:document.getElementById('args').value.split(' ').filter(Boolean),
   task_count:parseInt(document.getElementById('count').value)||1,timeout_secs:600,
   output_name:document.getElementById('outname').value.trim()};
  api('/v1/jobs',{method:'POST',body:JSON.stringify(body)}).then(function(r){
    log('Job <b>'+r.job_id+'</b> soumis ('+r.task_count+' taches).','ok');
    startTracking(body.id);
  }).catch(function(e){log('Echec soumission : '+e.message,'err');});
}
function jid(){return document.getElementById('jid').value.trim();}
function cmd_(){return document.getElementById('cmd').value.trim();}
function startTracking(job){
  document.getElementById('trackCard').style.display='block';
  document.getElementById('tjid').textContent=job;
  if(poll)clearInterval(poll);
  poll=setInterval(function(){tick(job);},2000); tick(job);
}
function tick(job){
  api('/v1/jobs/'+job+'/status').then(function(st){
    var total=0,done=0;for(var k in st.chunks){total+=st.chunks[k];}
    done=(st.chunks.done||0);
    var pct=total?Math.round(done*100/total):0;
    document.getElementById('pbar').style.width=pct+'%';
    var rows='<tr><th>Status</th><td>'+st.status+' ('+pct+'%)</td></tr>';
    for(var k in st.chunks){rows+='<tr><td>'+k+'</td><td>'+st.chunks[k]+'</td></tr>';}
    if(st.cost_eur!==undefined){rows+='<tr><td>Cout HT reel</td><td>'+st.cost_eur.toFixed(4)+' EUR</td></tr><tr><td>GPU-heures reelles</td><td>'+st.gpu_hours.toFixed(4)+'</td></tr>';}
    document.getElementById('stats').innerHTML=rows;
    if(st.status==='completed'){clearInterval(poll);poll=null;log('Job <b>'+job+'</b> termine. Cout reel : '+st.cost_eur.toFixed(4)+' EUR HT (vs AWS '+st.aws_equiv_eur.toFixed(2)+' EUR).','ok');refreshBalance();}
  }).catch(function(e){log('Suivi : '+e.message,'err');clearInterval(poll);poll=null;});
}
function refreshBalance(){
  api('/v1/balance').then(function(b){
    document.getElementById('bal').innerHTML='<p>Solde de credits : <b>'+b.balance_eur.toFixed(2)+' EUR</b></p>';
  }).catch(function(e){log('Solde : '+e.message,'err');});
}
log('Console prete. Renseignez votre cle API puis simulez puis soumettez.');
</script>
</body>
</html>`

// GET /console : sert la page (LAI-31).
func consolePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(consoleHTML))
}
