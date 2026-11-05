package main

// Sprint 17 (LAI-63) - Dashboard d'administration temps reel.
// Page /admin/dashboard, protegee par la cle X-Admin-Key (comme la
// console existante) :
//   - carte geographique des noeuds en ligne (Leaflet + ip-api),
//   - tableau de cartes par noeud : materiel, proprietaire, confiance,
//     travaux en cours, geolocalisation,
//   - jobs actifs avec progression et bouton Annuler,
//   - journal temps reel des actions (inscriptions, connexions,
//     enregistrements de noeuds, chunks, fraude, actions admin),
//   - boutons d'action : bloquer/debloquer un noeud, annuler un job,
//     recharger/rembourser des credits.
// Rafraichissement : polling auto toutes les 10 s.
// Le journal d'evenements vit en memoire (vide au redemarrage) : un
// vrai flux persiste sera ajoute avec l'observabilite de la beta.

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ------------------------------------------------------------------
// 1. Journal d'evenements (ring buffer memoire, 300 entrees)
// ------------------------------------------------------------------

// DashEvent : une ligne du journal temps reel.
type DashEvent struct {
	At   time.Time `json:"at"`
	Type string    `json:"type"`
	Msg  string    `json:"msg"`
}

type dashEventLog struct {
	mu   sync.Mutex
	evts []DashEvent
}

var dashEvents = &dashEventLog{}

// dashLog ajoute un evenement au journal (appele depuis toute la
// plateforme : hub, chunker, accounts, actions admin, fraude).
func dashLog(typ, msg string) {
	dashEvents.mu.Lock()
	defer dashEvents.mu.Unlock()
	dashEvents.evts = append(dashEvents.evts, DashEvent{At: time.Now(), Type: typ, Msg: msg})
	if len(dashEvents.evts) > 300 {
		dashEvents.evts = dashEvents.evts[len(dashEvents.evts)-300:]
	}
}

// dashRecent retourne les n derniers evenements (plus recents d'abord).
func dashRecent(n int) []DashEvent {
	dashEvents.mu.Lock()
	defer dashEvents.mu.Unlock()
	out := make([]DashEvent, 0, n)
	for i := len(dashEvents.evts) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, dashEvents.evts[i])
	}
	return out
}

// ------------------------------------------------------------------
// 2. Geolocalisation des noeuds (ip-api.com gratuit, sans cle)
// ------------------------------------------------------------------

type geoInfo struct {
	Country string  `json:"country"`
	City    string  `json:"city"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

var geoCache = struct {
	mu   sync.Mutex
	seen map[string]geoInfo
}{seen: make(map[string]geoInfo)}

// clientIP extrait l'IP reelle du client (X-Forwarded-For sous Caddy,
// RemoteAddr sinon).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// geoFor resout une IP en pays/ville/lat/lon (cachee en memoire).
// Les IP privees (dev local) ne donnent rien.
func geoFor(ip string) geoInfo {
	if ip == "" || net.ParseIP(ip) == nil {
		return geoInfo{Country: "inconnue"}
	}
	if p := net.ParseIP(ip); p.IsLoopback() || p.IsPrivate() {
		return geoInfo{Country: "reseau local"}
	}
	geoCache.mu.Lock()
	if g, ok := geoCache.seen[ip]; ok {
		geoCache.mu.Unlock()
		return g
	}
	geoCache.mu.Unlock()

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://ip-api.com/json/" + ip + "?fields=status,country,city,lat,lon")
	if err != nil {
		log.Printf("dash geo: %v", err)
		return geoInfo{Country: "inconnue"}
	}
	defer resp.Body.Close()
	var r struct {
		Status  string  `json:"status"`
		Country string  `json:"country"`
		City    string  `json:"city"`
		Lat     float64 `json:"lat"`
		Lon     float64 `json:"lon"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || r.Status != "success" {
		g := geoInfo{Country: "inconnue"}
		geoCache.mu.Lock()
		geoCache.seen[ip] = g
		geoCache.mu.Unlock()
		return g
	}
	g := geoInfo{Country: r.Country, City: r.City, Lat: r.Lat, Lon: r.Lon}
	geoCache.mu.Lock()
	geoCache.seen[ip] = g
	geoCache.mu.Unlock()
	return g
}

// ------------------------------------------------------------------
// 3. Vues de donnees pour le dashboard
// ------------------------------------------------------------------

// DashWork : un travail (chunk) rattache a un noeud ou a un job.
type DashWork struct {
	ChunkID string `json:"chunk_id"`
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
}

// DashNode : carte d'un noeud pour le tableau de bord.
type DashNode struct {
	ID          string    `json:"id"`
	OwnerEmail  string    `json:"owner_email"`
	GPUModel    string    `json:"gpu_model"`
	Category    string    `json:"gpu_category"`
	Zone        string    `json:"zone"`
	Status      string    `json:"status"`
	Trust       float64   `json:"trust"`
	Online      bool      `json:"online"`
	LastSeen    time.Time `json:"last_seen"`
	IP          string    `json:"ip"`
	Country     string    `json:"country"`
	City        string    `json:"city"`
	Lat         float64   `json:"lat"`
	Lon         float64   `json:"lon"`
	Work        []DashWork `json:"work"`
}

// DashJob : un job actif (au moins un chunk non termine).
type DashJob struct {
	JobID   string     `json:"job_id"`
	OrgID   string     `json:"org_id"`
	OrgName string     `json:"org_name"`
	Command string     `json:"command"`
	Queued  int        `json:"queued"`
	Running int        `json:"running"`
	Done    int        `json:"done"`
	Failed  int        `json:"failed"`
	Cancel  bool       `json:"cancelable"`
	Tasks   []DashWork `json:"tasks"`
}

// WorkOf liste les chunks non termines d'un noeud (file + en cours).
func (c *Chunker) WorkOf(nodeID string) []DashWork {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []DashWork
	for _, ch := range c.chunks {
		if ch.NodeID == nodeID && (ch.Status == "queued" || ch.Status == "running") {
			out = append(out, DashWork{ChunkID: ch.ID, JobID: ch.JobID, Status: ch.Status})
		}
	}
	return out
}

// dashJobs regroupe les jobs actifs avec l'etat de chaque chunk.
func dashJobs(c *Chunker, orgs *OrgStore) []DashJob {
	c.mu.Lock()
	defer c.mu.Unlock()
	byJob := make(map[string]*DashJob)
	order := make([]string, 0)
	for _, ch := range c.chunks {
		j, ok := byJob[ch.JobID]
		if !ok {
			j = &DashJob{JobID: ch.JobID, OrgID: ch.OrgID, Command: ch.Command}
			byJob[ch.JobID] = j
			order = append(order, ch.JobID)
		}
		j.Tasks = append(j.Tasks, DashWork{ChunkID: ch.ID, Status: ch.Status})
		switch ch.Status {
		case "queued":
			j.Queued++
		case "running":
			j.Running++
		case "done":
			j.Done++
		case "failed":
			j.Failed++
		}
	}
	var out []DashJob
	for _, id := range order {
		j := byJob[id]
		// Un job n'apparait que s'il reste quelque chose a suivre
		// (actif, ou termine il y a moins de 10 minutes).
		if j.Queued+j.Running == 0 && j.Done+j.Failed > 0 {
			continue
		}
		if j.OrgID != "" {
			if org := orgs.OrgByID(j.OrgID); org != nil {
				j.OrgName = org.Name
			}
		}
		j.Cancel = j.Queued > 0
		out = append(out, *j)
	}
	return out
}

// ReassignNode redonne a d'autres noeuds les chunks en file d'un
// noeud (utilise au blocage d'un noeud).
func (c *Chunker) ReassignNode(oldNodeID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	online := c.reg.OnlineNodes()
	n := 0
	for _, ch := range c.chunks {
		if ch.NodeID == oldNodeID && ch.Status == "queued" {
			if next := pickNode(online, ch); next != "" {
				ch.NodeID = next
				c.store.SaveChunk(ch)
				n++
			}
		}
	}
	return n
}

// GET /admin/dash/data : tout le dashboard en un seul appel JSON.
func adminDashData(reg *Registry, c *Chunker, f *Fraud, accounts *AccountStore, orgs *OrgStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		nodes := reg.AllNodes()
		out := make([]DashNode, 0, len(nodes))
		var online, banned int
		for _, n := range nodes {
			d := DashNode{
				ID: n.ID, GPUModel: n.GPUModel, Category: n.GPUCategory,
				Zone: n.Zone, Status: n.Status, Trust: n.TrustScore,
				Online: n.Online, LastSeen: n.LastSeen, IP: n.IP,
			}
			if n.Online {
				online++
			}
			if n.Status == "banned" {
				banned++
			}
			if n.UserID != "" {
				if u := accounts.UserByID(n.UserID); u != nil {
					d.OwnerEmail = u.Email
				}
			}
			g := geoFor(n.IP)
			d.Country, d.City, d.Lat, d.Lon = g.Country, g.City, g.Lat, g.Lon
			d.Work = c.WorkOf(n.ID)
			out = append(out, d)
		}
		done, failed, payoutEur := c.DashTotals()
		writeJSON(w, map[string]interface{}{
			"nodes":  out,
			"jobs":   dashJobs(c, orgs),
			"events": dashRecent(60),
			"fraud":  f.Alerts(),
			"stats": map[string]interface{}{
				"online":        online,
				"total":         len(nodes),
				"banned":        banned,
				"chunks_done":   done,
				"chunks_failed": failed,
				"payout_eur":    payoutEur,
			},
		})
	}
}

// DashTotals : taches reussies/echouees et total verse aux
// fournisseurs depuis le dernier redemarrage (+ restauration).
func (c *Chunker) DashTotals() (done, failed int, payoutEur float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.chunks {
		if ch.Status == "done" {
			done++
			payoutEur += ch.PayoutEur
		} else if ch.Status == "failed" {
			failed++
		}
	}
	return
}

// OrgByID retrouve une organisation cliente par son identifiant
// (affichage du nom dans le dashboard).
func (o *OrgStore) OrgByID(id string) *Org {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.orgs[id]
}

// ------------------------------------------------------------------
// 4. Actions admin
// ------------------------------------------------------------------

// POST /admin/dash/ban {node_id} : bloque un noeud. Il ne recoit plus
// aucun travail, ses chunks en file sont redistribues, son statut
// persiste en base (il survit aux redemarrages).
func adminDashBan(reg *Registry, c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			NodeID string `json:"node_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NodeID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if !reg.BanNode(req.NodeID) {
			http.Error(w, "noeud inconnu", http.StatusNotFound)
			return
		}
		n := c.ReassignNode(req.NodeID)
		dashLog("admin", fmt.Sprintf("noeud %s BLOQUE par l'admin (%d chunks redistribues)", req.NodeID, n))
		writeJSON(w, map[string]interface{}{"ok": true, "reassigned": n})
	}
}

// POST /admin/dash/unban {node_id} : debloque un noeud (confiance
// remise a 50, il doit se re-enregistrer pour revenir en ligne).
func adminDashUnban(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			NodeID string `json:"node_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.NodeID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		if !reg.UnbanNode(req.NodeID) {
			http.Error(w, "noeud inconnu", http.StatusNotFound)
			return
		}
		dashLog("admin", fmt.Sprintf("noeud %s debloque par l'admin", req.NodeID))
		writeJSON(w, map[string]interface{}{"ok": true})
	}
}

// POST /admin/dash/cancel {job_id} : annule les chunks en file d'un job.
func adminDashCancel(c *Chunker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var req struct {
			JobID string `json:"job_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.JobID == "" {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		n := c.CancelJob(req.JobID)
		dashLog("admin", fmt.Sprintf("job %s annule par l'admin (%d chunks en file annules)", req.JobID, n))
		writeJSON(w, map[string]interface{}{"ok": true, "canceled": n})
	}
}

// ------------------------------------------------------------------
// 5. Page HTML (Leaflet via CDN pour la carte)
// ------------------------------------------------------------------

const dashHTML = `<!DOCTYPE html>
<html lang="fr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>ComputeShare - Dashboard admin</title>
<link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css">
<style>
:root{color-scheme:dark}
*{box-sizing:border-box}
body{font-family:system-ui,Segoe UI,sans-serif;font-size:15px;background:#0f172a;color:#e2e8f0;margin:0;padding:16px}
h1{font-size:22px;margin:0 0 4px}
.muted{color:#94a3b8;font-size:13px}
.card{background:#1e293b;border-radius:10px;padding:14px;margin-top:12px}
.stats{display:flex;flex-wrap:wrap;gap:10px;margin-top:12px}
.stat{background:#1e293b;border-radius:10px;padding:10px 16px;min-width:130px}
.stat b{display:block;font-size:26px;color:#4ade80}
.stat .lbl{font-size:13px;color:#94a3b8}
.stat.warn b{color:#f87171}
.stat.blue b{color:#60a5fa}
input,button{font:inherit;border-radius:8px;border:1px solid #334155;background:#0f172a;color:#e2e8f0;padding:6px 10px}
button{cursor:pointer;background:#16a34a;border:none;color:#fff}
button.sec{background:#334155}
button.danger{background:#b91c1c}
.row{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-top:8px}
#mapid{height:340px;border-radius:10px;margin-top:10px;background:#1e293b}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(340px,1fr));gap:12px;margin-top:10px}
.ncard{background:#273449;border-radius:10px;padding:12px}
.ncard.off{opacity:.55}
.ncard.banned{border:1px solid #b91c1c}
.ncard h3{margin:0 0 8px;font-size:16px}
.ncard .muted{font-size:14px;line-height:1.5}
td,th{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:240px}
.badge{display:inline-block;padding:1px 8px;border-radius:99px;font-size:12px;background:#334155}
.badge.on{background:#16a34a}
.badge.off{background:#475569}
.badge.ban{background:#b91c1c}
.badge.warn{background:#b45309}
table{width:100%;border-collapse:collapse;margin-top:8px;font-size:13px}
td,th{padding:4px 8px;text-align:left;border-bottom:1px solid #334155}
.log{max-height:300px;overflow-y:auto;font-family:ui-monospace,monospace;font-size:13px;line-height:1.7}
.plus{color:#60a5fa}
.log .t{color:#64748b;margin-right:6px}
.log .type{display:inline-block;min-width:80px;font-weight:600}
.type-node_register{color:#60a5fa}.type-login{color:#c084fc}.type-signup{color:#c084fc}
.type-job_submit{color:#4ade80}.type-chunk_done{color:#4ade80}.type-chunk_failed{color:#f87171}
.type-fraud{color:#f87171}.type-admin{color:#fbbf24}.type-node_offline{color:#94a3b8}
h2{font-size:16px;margin:0}
</style>
</head>
<body>
<h1>&#129410; ComputeShare - Dashboard admin</h1>
<div class="row">
<input id="akey" type="password" placeholder="Cle admin" size="34">
<button id="save">Enregistrer la cle</button>
<button class="sec" id="refresh">Rafraichir</button>
<span class="muted" id="lastup"></span>
</div>
<div class="stats" id="stats"></div>

<div class="card">
<h2>&#128506; Carte des unites de puissance en ligne</h2>
<div id="mapid"></div>
</div>

<div class="card">
<h2>&#128187; Unites de puissance et leurs travaux</h2>
<div class="grid" id="board"></div>
</div>

<div class="card">
<h2>&#9881;&#65039; Jobs actifs</h2>
<div id="jobs" class="muted">Chargement...</div>
</div>

<div class="card">
<h2>&#128176; Credits des entreprises clientes</h2>
<div class="row">
<input id="orgid" placeholder="org-id (ex: org-87d96e97)" size="26">
<input id="amount" placeholder="Montant EUR" size="12">
<button id="topup">Recharger</button>
<button class="sec" id="refund">Rembourser</button>
<span id="billmsg" class="muted"></span>
</div>
</div>

<div class="card">
<h2>&#128220; Journal temps reel</h2>
<div class="log" id="log"></div>
</div>

<script src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js"></script>
<script>
var map=null, layer=null, timer=null;
function akey(){return localStorage.getItem('cs-admin-key')||'';}
function esc(s){var d=document.createElement('div');d.textContent=s==null?'':String(s);return d.innerHTML;}
function api(path, body){
  var opt={headers:{'X-Admin-Key':akey()}};
  if(body){opt.method='POST';opt.headers['Content-Type']='application/json';opt.body=JSON.stringify(body);}
  return fetch(path,opt).then(function(r){
    return r.text().then(function(t){
      if(!r.ok) throw new Error(r.status+' '+t);
      return t?JSON.parse(t):{};
    });
  });
}
function fmt(t){return new Date(t).toLocaleTimeString('fr-FR');}

function renderStats(s){
  var h='';
  h+='<div class="stat"><b>'+s.online+' <span style="font-size:15px;color:#94a3b8">/ '+s.total+'</span></b><span class="lbl">unites en ligne</span></div>';
  h+='<div class="stat blue"><b>'+(s.chunks_done)+'</b><span class="lbl">taches reussies</span></div>';
  h+='<div class="stat"><b>'+(s.payout_eur).toFixed(4)+' EUR</b><span class="lbl">verses aux fournisseurs</span></div>';
  h+='<div class="stat warn"><b>'+s.banned+'</b><span class="lbl">unites bloquees</span></div>';
  h+='<div class="stat warn"><b>'+(s.chunks_failed)+'</b><span class="lbl">taches echouees</span></div>';
  document.getElementById('stats').innerHTML=h;
}

function renderMap(nodes){
  if(typeof L==='undefined'){document.getElementById('mapid').innerHTML='<div class="muted" style="padding:40px;text-align:center">Carte indisponible (CDN non charge) - le reste du dashboard fonctionne.</div>';return;}
  if(!map){
    map=L.map('mapid').setView([46.5,2.5],4);
    L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png',{attribution:'OpenStreetMap'}).addTo(map);
    layer=L.layerGroup().addTo(map);
  }
  layer.clearLayers();
  nodes.forEach(function(n){
    if(!n.online||(!n.lat&&!n.lon))return;
    var m=L.circleMarker([n.lat,n.lon],{radius:9,color:'#16a34a',fillColor:'#16a34a',fillOpacity:.8});
    var pop='<b>'+esc(n.id)+'</b><br>'+esc(n.gpu_model)+'<br>'+esc(n.country||'')+(n.city?' - '+esc(n.city):'')+'<br>'+(n.work?n.work.length:0)+' travail(s) en cours';
    m.bindPopup(pop).addTo(layer);
  });
  // Recentre la carte sur les unites visibles (pas de vue "monde").
  var b=layer.getBounds();
  if(b.isValid()){map.fitBounds(b.pad(0.4),{maxZoom:7});}
}

function renderBoard(nodes){
  var h='';
  nodes.sort(function(a,b){return (b.online-a.online)||((b.work&&b.work.length||0)-(a.work&&a.work.length||0));});
  if(!nodes.length)h='<div class="muted">Aucun noeud connu.</div>';
  nodes.forEach(function(n){
    var cls='ncard'+(n.status==='banned'?' banned':(n.online?'':' off'));
    var badge=n.status==='banned'?'<span class="badge ban">BLOQUE</span>':(n.online?'<span class="badge on">En ligne</span>':'<span class="badge off">Hors ligne</span>');
    var trust=n.trust<50?'<span class="badge warn">confiance '+n.trust+'</span>':'confiance '+n.trust;
    h+='<div class="'+cls+'"><h3>'+esc(n.id)+' '+badge+'</h3>';
    h+='<div class="muted">'+esc(n.owner_email||'sans compte fournisseur')+' &#8226; '+esc(n.gpu_model)+' ('+esc(n.gpu_category)+') &#8226; zone '+esc(n.zone)+'</div>';
    h+='<div class="muted">'+esc(n.country||'')+(n.city?' - '+esc(n.city):'')+' &#8226; vu: '+fmt(n.last_seen)+' &#8226; '+trust+'</div>';
    var wl=n.work||[];
    if(wl.length){
      var maxw=Math.min(4,wl.length);
      h+='<table>';
      for(var i=0;i<maxw;i++){var w=wl[i];h+='<tr><td title="'+esc(w.chunk_id)+'">'+esc(w.job_id)+'</td><td>'+(w.status==='running'?'&#9654; en cours':'&#8987; en file')+'</td></tr>';}
      if(wl.length>maxw){h+='<tr><td colspan="2" class="plus">+ '+(wl.length-maxw)+' autres taches en file</td></tr>';}
      h+='</table>';
    }
    if(n.status==='banned'){
      h+='<div class="row"><button class="sec" onclick="doUnban(\''+esc(n.id)+'\')">Debloquer</button></div>';
    }else{
      h+='<div class="row"><button class="danger" onclick="doBan(\''+esc(n.id)+'\')">Bloquer ce noeud</button></div>';
    }
    h+='</div>';
  });
  document.getElementById('board').innerHTML=h;
}

function renderJobs(jobs){
  var el=document.getElementById('jobs');
  if(!jobs||!jobs.length){el.innerHTML='<span class="muted">Aucun job actif.</span>';return;}
  var h='<table><tr><th>Job</th><th>Entreprise</th><th>Commande</th><th>Etat</th><th></th></tr>';
  jobs.forEach(function(j){
    var st='';
    if(j.running)st+=j.running+' en cours ';
    if(j.queued)st+=j.queued+' en file ';
    if(j.done)st+=j.done+' OK ';
    if(j.failed)st+=j.failed+' echecs';
    h+='<tr><td>'+esc(j.job_id)+'</td><td>'+esc(j.org_name||j.org_id||'interne')+'</td><td>'+esc(j.command)+'</td><td>'+esc(st)+'</td>';
    h+='<td>'+(j.cancel?'<button class="danger" onclick="doCancel(\''+esc(j.job_id)+'\')">Annuler la file</button>':'')+'</td></tr>';
  });
  h+='</table>';
  el.innerHTML=h;
}

function renderLog(events){
  var h='';
  (events||[]).forEach(function(e){
    h+='<div><span class="t">'+fmt(e.at)+'</span><span class="type type-'+esc(e.type)+'">'+esc(e.type)+'</span> '+esc(e.msg)+'</div>';
  });
  document.getElementById('log').innerHTML=h||'<div class="muted">Journal vide (redemarrage recent).</div>';
}

function safe(fn){try{return fn();}catch(e){console.error(e);}}
function load(){
  api('/admin/dash/data').then(function(d){
    safe(function(){renderStats(d.stats||{});});
    safe(function(){renderMap(d.nodes||[]);});
    safe(function(){renderBoard(d.nodes||[]);});
    safe(function(){renderJobs(d.jobs||[]);});
    safe(function(){renderLog(d.events||[]);});
    document.getElementById('lastup').textContent='Derniere mise a jour: '+new Date().toLocaleTimeString('fr-FR');
  }).catch(function(e){
    document.getElementById('lastup').innerHTML='<span style="color:#f87171">'+esc(String(e))+'</span>';
  });
}

function doBan(id){if(!confirm('Bloquer '+id+' ? Il ne recevra plus aucun travail.'))return;api('/admin/dash/ban',{node_id:id}).then(load).catch(showErr);}
function doUnban(id){if(!confirm('Debloquer '+id+' ?'))return;api('/admin/dash/unban',{node_id:id}).then(load).catch(showErr);}
function doCancel(job){if(!confirm('Annuler les taches en file du job '+job+' ?'))return;api('/admin/dash/cancel',{job_id:job}).then(load).catch(showErr);}
function showErr(e){document.getElementById('billmsg').innerHTML='<span style="color:#f87171">'+esc(String(e))+'</span>';}

document.getElementById('save').onclick=function(){
  localStorage.setItem('cs-admin-key',document.getElementById('akey').value.trim());
  document.getElementById('lastup').textContent='Cle enregistree.';
  load();
};
document.getElementById('refresh').onclick=load;
document.getElementById('topup').onclick=function(){
  api('/admin/billing/topup',{org_id:document.getElementById('orgid').value.trim(),amount_eur:parseFloat(document.getElementById('amount').value)}).then(function(){document.getElementById('billmsg').textContent='Credits recharges.';load();}).catch(showErr);
};
document.getElementById('refund').onclick=function(){
  api('/admin/billing/refund',{org_id:document.getElementById('orgid').value.trim(),amount_eur:parseFloat(document.getElementById('amount').value),ref:'remboursement manuel dashboard'}).then(function(){document.getElementById('billmsg').textContent='Remboursement effectue.';load();}).catch(showErr);
};

if(akey()){document.getElementById('akey').value=akey();load();}
if(timer)clearInterval(timer);
timer=setInterval(load,10000);
</script>
</body>
</html>`

// GET /admin/dashboard : la page (les donnees exigent la cle admin).
func dashboardPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashHTML))
}
