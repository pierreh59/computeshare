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

// emailShell : gabarit HTML commun des e-mails (fond clair, bouton).
func emailShell(intro, actionURL, actionLabel, outro string) string {
	return `<!DOCTYPE html><html lang="fr"><body style="font-family:Arial,Helvetica,sans-serif;background:#f8fafc;padding:24px;color:#0f172a">
<div style="max-width:520px;margin:auto;background:#ffffff;border-radius:12px;padding:32px;border:1px solid #e2e8f0">
<h2 style="margin-top:0">🥗 ComputeShare</h2>
<p>` + intro + `</p>
<p style="text-align:center;margin:28px 0">
<a href="` + actionURL + `" style="background:#0ea5e9;color:#ffffff;padding:12px 24px;border-radius:8px;text-decoration:none">` + actionLabel + `</a>
</p>
<p style="color:#64748b;font-size:13px">Lien valable 1 heure. Si vous n'êtes pas à l'origine de cette action, ignorez cet e-mail.</p>
<p>` + outro + `</p>
</div></body></html>`
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
