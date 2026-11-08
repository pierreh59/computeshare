package main

// Sprint 32 : bilan d'activite hebdomadaire envoye par e-mail a
// l'operateur. Deux declencheurs :
//  1. automatique : chaque lundi matin a 9h (une seule fois par
//     semaine), si la variable REPORT_EMAIL est definie ;
//  2. a la demande : POST /admin/report/email (cle X-Admin-Key),
//     bouton "Bilan d'activite" du dashboard admin.
// Chiffres : journal de facturation (heures GPU vendues, EUR
// factures HT, TVA, rechargements, taches reussies, clients actifs)
// + grand livre du wallet (versements fournisseurs, noeuds actifs)
// + comparaison AWS on-demand (economie realisée par les clients).

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

// Reporter : assemble le bilan et l'envoie par e-mail.
type Reporter struct {
	billing  *Billing
	wallet   *Wallet
	reg      *Registry
	emails   *Emailer
	lastWeek string // lundi deja traite (format 2006-01-02)
}

func NewReporter(b *Billing, w *Wallet, r *Registry, e *Emailer) *Reporter {
	return &Reporter{billing: b, wallet: w, reg: r, emails: e}
}

// round6 : arrondi a la micro-unite (affichage stable des petits montants).
func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// asFloat : conversion best-effort d'une valeur de stats.
func asFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

// BuildReport : agregats des N derniers jours (defaut 7, max 90).
func (rp *Reporter) BuildReport(days int) map[string]interface{} {
	if days <= 0 || days > 90 {
		days = 7
	}
	now := time.Now()
	since := now.AddDate(0, 0, -days)
	hours, spentHT, vat, topups, taches, orgs := rp.billing.ReportSince(since)
	payouts, nodes := rp.wallet.PayoutsSince(since)
	aws := hours * awsCompareEurPerHour
	return map[string]interface{}{
		"periode_jours":          days,
		"du":                     since.Format("02/01/2006"),
		"au":                     now.Format("02/01/2006"),
		"heures_vendues":         round6(hours),
		"taches_reussies":        taches,
		"facture_ht_eur":         round6(spentHT),
		"tva_eur":                round6(vat),
		"verse_fournisseurs_eur": round6(payouts),
		"noeuds_actifs":          len(nodes),
		"noeuds_en_ligne":        len(rp.reg.OnlineNodes()),
		"clients_actifs":         len(orgs),
		"rechargements_eur":      round6(topups),
		"equivalent_aws_eur":     round6(aws),
		"economie_clients_eur":   round6(aws - spentHT),
	}
}

// StartWeekly : envoi automatique chaque lundi matin, une seule fois
// par semaine (tick 30 min, garde anti-doublon sur la date du lundi).
func (rp *Reporter) StartWeekly() {
	go func() {
		for range time.Tick(30 * time.Minute) {
			to := strings.TrimSpace(os.Getenv("REPORT_EMAIL"))
			if to == "" {
				continue
			}
			now := time.Now()
			if now.Weekday() != time.Monday || now.Hour() != 9 {
				continue
			}
			week := now.Format("2006-01-02")
			if week == rp.lastWeek {
				continue
			}
			rp.lastWeek = week
			stats := rp.BuildReport(7)
			dashLog("admin", "bilan hebdo envoye automatiquement a "+to)
			rp.emails.SendReport(to, stats)
		}
	}()
}

// adminReportEmail : POST /admin/report/email - envoie le bilan a la
// demande. Body optionnel : {"email":"...","days":7}. Sans e-mail,
// on utilise REPORT_EMAIL ; sans les deux : erreur 400.
func adminReportEmail(rp *Reporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Email string `json:"email"`
			Days  int    `json:"days"`
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "body invalide", http.StatusBadRequest)
				return
			}
		}
		to := strings.TrimSpace(req.Email)
		if to == "" {
			to = strings.TrimSpace(os.Getenv("REPORT_EMAIL"))
		}
		if to == "" || !strings.Contains(to, "@") {
			http.Error(w, "destinataire manquant : passez email ou definissez REPORT_EMAIL", http.StatusBadRequest)
			return
		}
		stats := rp.BuildReport(req.Days)
		dashLog("admin", "bilan envoye a la demande a "+to)
		go rp.emails.SendReport(to, stats)
		writeJSON(w, stats)
	}
}
