package main

// Sprint 29 : envoi des e-mails transactionnels (verification
// d'adresse, reinitialisation de mot de passe) via l'API Brevo.
// Si BREVO_API_KEY est absente : aucun envoi reel, l'URL est
// journalisee a la place (mode teste avant la configuration Brevo).
// Environnement :
//   BREVO_API_KEY      : cle API Brevo (obligatoire pour l'envoi)
//   BREVO_SENDER_EMAIL : expediteur (defaut noreply@computeshare.fr)
//   BREVO_SENDER_NAME  : nom de l'expediteur (defaut ComputeShare)
//   PUBLIC_BASE_URL    : base des liens des e-mails

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type Emailer struct {
	apiKey      string
	senderName  string
	senderEmail string
	baseURL     string
}

func envDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func NewEmailer() *Emailer {
	return &Emailer{
		apiKey:      strings.TrimSpace(os.Getenv("BREVO_API_KEY")),
		senderName:  envDefault("BREVO_SENDER_NAME", "ComputeShare"),
		senderEmail: envDefault("BREVO_SENDER_EMAIL", "noreply@computeshare.fr"),
		baseURL:     strings.TrimRight(envDefault("PUBLIC_BASE_URL", "https://api.computeshare.fr"), "/"),
	}
}

// configured : true si Brevo est operationnel.
func (e *Emailer) configured() bool { return e != nil && e.apiKey != "" }

// send : POST vers l'API transactionnelle Brevo.
func (e *Emailer) send(to, subject, html string) error {
	if !e.configured() {
		return errors.New("BREVO_API_KEY absente : e-mail non envoye")
	}
	payload := map[string]interface{}{
		"sender":      map[string]string{"name": e.senderName, "email": e.senderEmail},
		"to":          []map[string]string{{"email": to}},
		"subject":     subject,
		"htmlContent": html,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", "https://api.brevo.com/v3/smtp/email", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("api-key", e.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("brevo HTTP %d : %s", resp.StatusCode, string(body))
	}
	return nil
}

// emailShellNote : gabarit HTML commun des e-mails (fond clair,
// bouton). note = la petite ligne grise sous le bouton (differe selon
// le type d e-mail).
func emailShellNote(note, intro, actionURL, actionLabel, outro string) string {
	return `<!DOCTYPE html><html lang="fr"><body style="font-family:Arial,Helvetica,sans-serif;background:#f8fafc;padding:24px;color:#0f172a">
<div style="max-width:520px;margin:auto;background:#ffffff;border-radius:12px;padding:32px;border:1px solid #e2e8f0">
<h2 style="margin-top:0">🥗 ComputeShare</h2>
<p>` + intro + `</p>
<p style="text-align:center;margin:28px 0">
<a href="` + actionURL + `" style="background:#0ea5e9;color:#ffffff;padding:12px 24px;border-radius:8px;text-decoration:none">` + actionLabel + `</a>
</p>
<p style="color:#64748b;font-size:13px">` + note + `</p>
<p>` + outro + `</p>
</div></body></html>`
}

// emailShell : gabarit des e-mails a lien temporaire (verification,
// reinitialisation) - lien valable 1 heure.
func emailShell(intro, actionURL, actionLabel, outro string) string {
	return emailShellNote("Lien valable 1 heure. Si vous n'êtes pas à l'origine de cette action, ignorez cet e-mail.",
		intro, actionURL, actionLabel, outro)
}

// SendVerification : e-mail de verification d'adresse (inscription ou
// renvoi). En cas d'echec d'envoi, l'URL est journalisee (secours
// de diagnostic).
func (e *Emailer) SendVerification(email, token string) {
	url := e.baseURL + "/verify?token=" + token
	html := emailShell(
		"Bonjour,<br><br>Bienvenue sur ComputeShare ! Cliquez sur le bouton ci-dessous pour vérifier votre adresse e-mail :",
		url, "Vérifier mon adresse",
		"Cette vérification est nécessaire pour soumettre des jobs. Le lien expire dans 1 heure ; vous pouvez en demander un nouveau depuis votre console.")
	if err := e.send(email, "Vérifiez votre adresse e-mail - ComputeShare", html); err != nil {
		log.Printf("email: verification non envoyee a %s : %v - lien de secours %s", email, err, url)
	} else {
		log.Printf("email: verification envoyee a %s", email)
	}
}

// SendReset : e-mail de reinitialisation de mot de passe.
func (e *Emailer) SendReset(email, token string) {
	url := e.baseURL + "/reset?token=" + token
	html := emailShell(
		"Bonjour,<br><br>Une demande de réinitialisation de votre mot de passe ComputeShare a été faite. Cliquez ci-dessous pour en choisir un nouveau :",
		url, "Choisir un nouveau mot de passe",
		"Votre mot de passe actuel restera inchangé tant que vous n'en définissez pas un nouveau. Le lien expire dans 1 heure.")
	if err := e.send(email, "Réinitialisation de votre mot de passe - ComputeShare", html); err != nil {
		log.Printf("email: reset non envoye a %s : %v - lien de secours %s", email, err, url)
	} else {
		log.Printf("email: reset envoye a %s", email)
	}
}

// SendJobDone : e-mail "votre job est termine" (Sprint 31). Envoi en
// goroutine depuis le Notifier - ne bloque jamais la completion.
func (e *Emailer) SendJobDone(email, jobID string, done, failed int, costEur float64) {
	url := e.baseURL + "/me"
	intro := fmt.Sprintf("Bonjour,<br><br>Votre job <b>%s</b> est terminé : <b>%d</b> tâche(s) réussie(s)", jobID, done)
	if failed > 0 {
		intro += fmt.Sprintf(" et <b>%d</b> en échec", failed)
	}
	intro += fmt.Sprintf(".<br>Coût réel facturé : <b>%.6f EUR HT</b>.", costEur)
	html := emailShellNote(
		"Vous recevez cet e-mail car vous avez un compte client ComputeShare. Vous pouvez désactiver ces notifications depuis votre console.",
		intro, url, "Voir mes résultats",
		"Les fichiers de résultats restent téléchargeables 30 jours depuis votre console.")
	if err := e.send(email, "Votre job "+jobID+" est terminé - ComputeShare", html); err != nil {
		log.Printf("email: job termine non envoye a %s : %v", email, err)
	} else {
		log.Printf("email: job termine envoye a %s (job %s)", email, jobID)
	}
}

// SendLowCredits : e-mail "vos credits sont bas" (Sprint 31, seuil
// produit = 5 EUR). Une seule alerte jusqu au rechargement.
func (e *Emailer) SendLowCredits(email string, balance float64) {
	url := e.baseURL + "/me"
	html := emailShellNote(
		"Vous recevez cet e-mail car vous avez un compte client ComputeShare. Vous pouvez désactiver ces notifications depuis votre console.",
		fmt.Sprintf("Bonjour,<br><br>Le solde de crédits de votre compte est bas : il reste <b>%.2f EUR</b>.<br>Pour que vos jobs continuent sans interruption, pensez à recharger votre compte.", balance),
		url, "Ouvrir ma console",
		"En bêta, le rechargement de crédits se fait avec l'équipe ComputeShare : répondez simplement à cet e-mail.")
	if err := e.send(email, "Vos crédits ComputeShare sont bas - ComputeShare", html); err != nil {
		log.Printf("email: credits bas non envoye a %s : %v", email, err)
	} else {
		log.Printf("email: credits bas envoye a %s", email)
	}
}

// SendReport : bilan d'activite hebdo pour l'operateur (Sprint 32).
func (e *Emailer) SendReport(to string, s map[string]interface{}) {
	days := int(asFloat(s["periode_jours"]))
	row := func(label, value string) string {
		return "<tr><td style=\"padding:6px 12px;border-bottom:1px solid #e2e8f0;color:#64748b\">" + label +
			"</td><td style=\"padding:6px 12px;border-bottom:1px solid #e2e8f0;text-align:right\"><b>" + value + "</b></td></tr>"
	}
	n := func(k string) string { return fmt.Sprintf("%.4f", asFloat(s[k])) }
	table := row("Heures de calcul vendues", fmt.Sprintf("%.4f h GPU", asFloat(s["heures_vendues"])))
	table += row("Tâches réussies", fmt.Sprintf("%d", int(asFloat(s["taches_reussies"]))))
	table += row("Facturé aux clients (HT)", n("facture_ht_eur")+" EUR")
	table += row("dont TVA collectée", n("tva_eur")+" EUR")
	table += row("Rechargements de crédits", n("rechargements_eur")+" EUR")
	table += row("Versé aux fournisseurs", n("verse_fournisseurs_eur")+" EUR")
	table += row("Clients actifs", fmt.Sprintf("%d", int(asFloat(s["clients_actifs"]))))
	table += row("Nœuds fournisseurs actifs", fmt.Sprintf("%d", int(asFloat(s["noeuds_actifs"]))))
	table += row("Nœuds en ligne actuellement", fmt.Sprintf("%d", int(asFloat(s["noeuds_en_ligne"]))))
	table += row("Équivalent AWS on-demand", n("equivalent_aws_eur")+" EUR")
	table += row("Économie réalisée par les clients", n("economie_clients_eur")+" EUR")
	html := "<!DOCTYPE html><html lang=\"fr\"><body style=\"font-family:Arial,Helvetica,sans-serif;background:#f8fafc;padding:24px;color:#0f172a\">" +
		"<div style=\"max-width:560px;margin:auto;background:#ffffff;border-radius:12px;padding:32px;border:1px solid #e2e8f0\">" +
		"<h2 style=\"margin-top:0\">🥗 ComputeShare</h2>" +
		fmt.Sprintf("<p>Bilan d'activité du <b>%v</b> au <b>%v</b> :</p>", s["du"], s["au"]) +
		"<table style=\"width:100%;border-collapse:collapse\">" + table + "</table>" +
		"<p style=\"color:#64748b;font-size:13px\">Bilan envoyé automatiquement chaque lundi matin, ou à la demande depuis le dashboard admin.</p>" +
		"</div></body></html>"
	subject := fmt.Sprintf("Bilan ComputeShare - %d derniers jours", days)
	if err := e.send(to, subject, html); err != nil {
		log.Printf("email: bilan non envoye a %s : %v", to, err)
	} else {
		log.Printf("email: bilan envoye a %s (%d jours)", to, days)
	}
}
