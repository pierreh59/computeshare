package main

// Sprint 37 : assistant conversationnel en ligne (bulle de chat).
//
// Objectif produit : repondre en francais simple aux visiteurs (vitrine),
// clients et fournisseurs ; faire un premier diagnostic ; une fois
// connecte, consulter les donnees du compte (jobs, soldes, noeuds) et
// proposer des actions automatisees (renvoi d e-mail de verification,
// historique par e-mail...).
//
// Regles de confidentialite :
//   - l assistant ne voit QUE le compte connecte, jamais les autres ;
//   - aucun secret, port, chemin de fichier ou detail d infrastructure
//     n apparait dans les reponses (garde-fou dans le prompt systeme
//     et dans les reponses FAQ pre-ecrites) ;
//   - les actions demandent une session valide et sont tracees dans
//     le journal admin (dashLog type "chat").
//
// Mode degrade : sans MISTRAL_API_KEY, l assistant repond uniquement
// via la FAQ interne (reponses pre-ecrites, gratuites). Avec la cle,
// les questions hors FAQ passent par l IA generative (Mistral).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ------------------------------------------------------------------
// Structure et construction

type ChatBot struct {
	accounts *AccountStore
	chunker  *Chunker
	billing  *Billing
	wallet   *Wallet
	registry *Registry
	emails   *Emailer

	apiKey string // MISTRAL_API_KEY (vide = mode FAQ seule)
	model  string

	// anti-abus : fenetre glissante par cle (IP ou utilisateur).
	mu    sync.Mutex
	hits  map[string][]time.Time
}

func NewChatBot(accounts *AccountStore, chunker *Chunker, billing *Billing, wallet *Wallet, reg *Registry, emails *Emailer) *ChatBot {
	b := &ChatBot{
		accounts: accounts,
		chunker:  chunker,
		billing:  billing,
		wallet:   wallet,
		registry: reg,
		emails:   emails,
		apiKey:   os.Getenv("MISTRAL_API_KEY"),
		model:    os.Getenv("MISTRAL_CHAT_MODEL"),
		hits:     map[string][]time.Time{},
	}
	if b.model == "" {
		b.model = "mistral-small-latest"
	}
	return b
}

// ------------------------------------------------------------------
// FAQ interne : reponses pre-ecrites pour les cas frequents.

type chatFAQ struct {
	keywords []string
	answer   string
	actions  []string // ids d actions proposees en bouton (si autorisees)
}

var chatFAQs = []chatFAQ{
	{
		keywords: []string{"job bloque", "bloque", "coince", "stuck", "relancer", "relance"},
		answer: "Un job peut sembler bloqué pour plusieurs raisons, la plus fréquente étant l'absence du fichier résultat : chaque job doit écrire son fichier de sortie avec le nom indiqué dans le champ « Nom du fichier résultat ». Si le message est « commande hors allowlist », c'est le nom exact de la commande qui est en cause. Vérifiez aussi le délai maximum par tâche : une tâche trop longue est interrompue sans être facturée. Connectez-vous et décrivez-moi votre job, je peux consulter son état précis.",
	},
	{
		keywords: []string{"fichier resultat", "resultat manquant", "bad_output", "sortie", "output_name"},
		answer: "Si votre job échoue avec « fichier résultat manquant » : votre commande doit écrire elle-même le fichier de sortie dans son dossier de travail, avec EXACTEMENT le nom renseigné dans « Nom du fichier résultat » du formulaire (par exemple : sh -c \"cat entree.txt > sortie.txt\" avec sortie.txt en nom de résultat). Un affichage à l'écran seul ne suffit pas. Les jobs échoués ne sont jamais facturés.",
	},
	{
		keywords: []string{"verifie", "verification", "e-mail", "email de verification", "mail pas recu", "mail recu", "verifier mon adresse"},
		answer: "Pour soumettre un job, votre adresse e-mail doit être vérifiée (clic sur le lien reçu à l'inscription, valable 1 heure). Vérifiez le dossier spam, ou connectez-vous : je peux vous renvoyer l'e-mail de vérification immédiatement.",
		actions: []string{"resend_verification"},
	},
	{
		keywords: []string{"credits", "solde", "recharger", "solde insuffisant", "facture", "prix", "tarif", "coute", "combien"},
		answer: "Vous êtes facturé uniquement au temps de calcul réellement utilisé (à la seconde), environ 30 % moins cher que le cloud classique. Le téléchargement de l'image de votre conteneur est facturé à 20 % du tarif pendant le calcul. Un échec n'est jamais facturé. Pour recharger vos crédits en bêta, contactez l'équipe depuis votre console. Connectez-vous et je peux vous indiquer votre solde exact.",
	},
	{
		keywords: []string{"noeud", "no available", "503", "machine disponible", "personne ne prend", "pas de machine"},
		answer: "Si le message indique qu'aucune machine n'est disponible : le nombre de fournisseurs connectés fluctue (nous sommes en bêta). Attendez quelques minutes et relancez, votre job partira dès qu'une machine adaptée se libère. Pour un job GPU, il faut qu'un fournisseur avec une carte graphique soit en ligne.",
	},
	{
		keywords: []string{"fournisseur", "vendre", "gagner", "gains", "louer mon pc", "mon ordinateur", "miner", "minage"},
		answer: "Vous pouvez mettre votre PC ou serveur à disposition et être payé au temps de calcul réellement effectué (le temps de téléchargement des images n'est pas payé, c'est la marge plateforme). Il suffit de l'application ComputeShare pour Windows, de votre e-mail et mot de passe. Docker Desktop est installé automatiquement et doit être validé une seule fois. Les gains s'accumulent dans votre portefeuille et sont retirable après vérification d'identité.",
	},
	{
		keywords: []string{"docker", "conteneur", "container", "wsl"},
		answer: "Les jobs conteneurs s'exécutent dans Docker, isolés du reste de votre machine : sans accès Internet, sans droits supplémentaires, limités en mémoire et en processeur, et les fichiers sont effacés après chaque job. Chez le fournisseur : installez l'application, Docker Desktop est proposé automatiquement (à valider une seule fois au premier lancement). Chez le client : vous fournissez l'adresse de votre image conteneur (Docker Hub, GHCR...) et votre commande.",
	},
	{
		keywords: []string{"retrait", "retirer", "kyc", "virement", "paiement fournisseur", "withdraw"},
		answer: "Les gains des fournisseurs sont retirable depuis l'application. Pour les comptes particuliers non vérifiés, des plafonds de bêta s'appliquent (100 EUR sur 30 jours, 500 EUR cumulés) ; un compte société avec SIRET vérifié n'a pas de plafond. La vérification d'identité (KYC) est demandée avant le premier retrait.",
	},
	{
		keywords: []string{"siret", "societe", "entreprise", "particulier", "plafond", "tva"},
		answer: "Deux types de compte : particulier (par défaut) ou société. Une société renseigne sa raison sociale, son SIRET (vérifié automatiquement auprès de l'INSEE), son numéro de TVA intracommunautaire et son adresse. Une fois le SIRET vérifié, aucun plafond de bêta ne s'applique. Vous pouvez changer de statut à tout moment depuis votre console.",
	},
	{
		keywords: []string{"confidentialite", "rgpd", "donnees", "protection", "cgu", "vieux droit"},
		answer: "Vos données sont hébergées en France. Vous pouvez à tout moment exporter l'intégralité de votre compte (bouton dans votre console) ou supprimer votre compte : il est anonymisé et vos machines sont détachées. Les fichiers de travail transitent par la plateforme mais ne sont conservés que le temps du job. Le consentement à nos conditions est demandé et horodaté à l'inscription.",
	},
	{
		keywords: []string{"notification", "e-mail de fin", "previens", "alerte", "prevenir"},
		answer: "Vous recevez un e-mail quand un job se termine et quand vos crédits passent sous 5 EUR. Ces notifications sont désactivables à tout moment depuis votre console (carte Notifications par e-mail).",
	},
	{
		keywords: []string{"gpu", "carte graphique", "cuda", "ia", "intelligence artificielle"},
		answer: "Oui, la plateforme propose des jobs GPU : votre image conteneur doit être conçue pour CUDA (par exemple une image PyTorch CUDA). Le job part uniquement si un fournisseur avec une carte graphique est en ligne ; la facturation suit la catégorie de sa carte. Les outils basés Vulkan ne sont pas encore supportés en bêta.",
	},
	{
		keywords: []string{"mettre a jour", "mise a jour", "application", "installer", "telecharger l application", "app ne repond pas"},
		answer: "L'application se met à jour toute seule : cliquez sur « Rechercher les mises à jour » dans l'en-tête, l'installation et le redémarrage sont automatiques. En cas de blocage réseau, l'installateur se télécharge aussi manuellement depuis la page des versions du projet, et s'installe par-dessus l'existante sans rien perdre.",
	},
	{
		keywords: []string{"contact", "humain", "support", "parler a quelqu un", "assistance", "help"},
		answer: "Je suis l'assistant ComputeShare et je réponds aux questions courantes 24h/24. Pour une demande commerciale, de facturation ou un problème que je ne résous pas : écrivez à contact@computeshare.fr en précisant votre e-mail de compte, l'équipe vous répondra.",
	},
	{
		keywords: []string{"historique", "recapitulatif", "recap", "mes jobs", "releve"},
		answer: "Vous pouvez consulter tout votre historique depuis votre console. Connectez-vous et demandez-moi « envoie-moi mon historique » : je peux vous envoyer un récapitulatif complet par e-mail.",
		actions: []string{"history_email"},
	},
}

// normaliseChat : minuscules + suppression des accents pour un
// rapprochement simple entre la question et les mots-cles.
func normaliseChat(s string) string {
	r := strings.ToLower(s)
	repl := strings.NewReplacer(
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"à", "a", "â", "a", "ä", "a",
		"î", "i", "ï", "i", "ì", "i",
		"ô", "o", "ö", "o", "ò", "o",
		"û", "u", "ü", "u", "ù", "u",
		"ç", "c", "œ", "oe", "'", " ", "’", " ",
	)
	return repl.Replace(r)
}

// matchFAQ retourne la meilleure entrée FAQ (au moins un mot-cle
// trouve dans le message), ou nil.
func matchFAQ(msg string) *chatFAQ {
	m := normaliseChat(msg)
	best := (*chatFAQ)(nil)
	bestScore := 0
	for i := range chatFAQs {
		score := 0
		for _, k := range chatFAQs[i].keywords {
			if strings.Contains(m, normaliseChat(k)) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			best = &chatFAQs[i]
		}
	}
	if bestScore == 0 {
		return nil
	}
	return best
}

// ------------------------------------------------------------------
// Anti-abus : fenetre glissante par cle.

func (bot *ChatBot) allow(key string, limit int) bool {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	now := time.Now()
	fresh := bot.hits[key][:0]
	for _, t := range bot.hits[key] {
		if now.Sub(t) < 10*time.Minute {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= limit {
		bot.hits[key] = fresh
		return false
	}
	bot.hits[key] = append(fresh, now)
	return true
}

// ------------------------------------------------------------------
// Contexte du compte connecte (injecte dans le prompt IA et utilise
// par les reponses deterministes). Jamais de secret ici.

func (bot *ChatBot) userContext(u *User) string {
	if u == nil {
		return "Utilisateur NON connecte : reponses generiques uniquement, propose de se connecter pour un diagnostic personnalise."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Utilisateur connecte : %s, role %s", u.DisplayName, mapClientSeller(u.Role))
	if !u.EmailVerified {
		b.WriteString(", adresse e-mail NON verifiee (action resend_verification disponible)")
	} else {
		b.WriteString(", adresse e-mail verifiee")
	}
	if u.AccountType == "societe" {
		b.WriteString(", compte societe (" + u.CompanyName + ")")
		if u.SiretVerified {
			b.WriteString(" SIRET verifie")
		} else {
			b.WriteString(" SIRET non verifie (plafonds de beta applicables)")
		}
	} else {
		b.WriteString(", compte particulier (plafonds de beta applicables)")
	}
	if u.Role == "client" && u.OrgID != "" {
		fmt.Fprintf(&b, ". Solde de credits : %.2f EUR", bot.billing.Balance(u.OrgID))
		jobs := bot.chunker.OrgJobs(u.OrgID, 10)
		if len(jobs) == 0 {
			b.WriteString(". Aucun job soumis pour le moment")
		} else {
			b.WriteString(". Derniers jobs :")
			for _, j := range jobs {
				st, _ := j["chunks"].(map[string]int)
				fmt.Fprintf(&b, " %s (%d taches : %d reussies, %d echouees)", j["job_id"], j["task_count"], st["done"], st["failed"])
			}
		}
	}
	if u.Role == "seller" {
		nodes := bot.registry.NodesOfUser(u.ID)
		total := 0.0
		for _, n := range nodes {
			total += bot.wallet.Balance(n.ID)
		}
		fmt.Fprintf(&b, ". Noeuds fournisseur : %d, gains cumules : %.4f EUR", len(nodes), total)
		for _, n := range nodes {
			state := "hors ligne"
			if n.Online {
				state = "en ligne"
			}
			fmt.Fprintf(&b, " [%s : %s, GPU %s, solde %.4f EUR]", n.ID, state, n.GPUModel, bot.wallet.Balance(n.ID))
		}
		if len(nodes) > 0 {
			b.WriteString(". Derniers versements du premier noeud :")
			h := bot.wallet.History(nodes[0].ID)
			count := 0
			for i := len(h) - 1; i >= 0 && count < 5; i-- {
				if h[i].EntryType == "payout" {
					fmt.Fprintf(&b, " %.4f EUR", h[i].AmountEur)
					count++
				}
			}
		}
	}
	return b.String()
}

// vocabulaire externe : client / fournisseur (les roles internes
// restent client | seller).
func mapClientSeller(role string) string {
	if role == "seller" {
		return "fournisseur"
	}
	return "client"
}

// ------------------------------------------------------------------
// Prompt systeme de l IA (garde-fous de confidentialite inclus).

const chatSystemPrompt = `Tu es l'assistant en ligne de ComputeShare, une plateforme française de calcul partagé : les entreprises clientes soumettent des travaux de calcul (encodage vidéo, rendu 3D, intelligence artificielle...) qui s'exécutent sur les PC mis à disposition par des fournisseurs particuliers ou entreprises, payés au temps de calcul réel.

RÈGLES ABSOLUES :
- Réponds en français, en langage simple et concret, comme à un non-technicien. Reste court (5 phrases maximum en général).
- Ne révèle JAMAIS d'informations techniques internes : pas de noms de fichiers serveur, ports, bases de données, adresses d'infrastructure, clés ou jetons, noms de technologies côté plateforme. Docker peut être mentionné uniquement pour expliquer l'installation chez le fournisseur.
- Ne parle jamais des autres comptes. Ne devine pas de montants : utilise le contexte fourni.
- Pour toute question de facturation, de litige, de paiement ou de données personnelles complexes : invite à écrire à contact@computeshare.fr.
- Si tu n'es pas sûr, dis-le et propose l'escalade vers l'équipe plutôt qu'une invention.
- Tu ne peux proposer QUE les actions listées dans ACTIONS DISPONIBLES. Pour en proposer une, termine ta réponse par une ligne unique « ACTION: <id> » (sans autre texte après).

CONNAISSANCES PRODUIT :
- Client : création de compte gratuite, vérification e-mail obligatoire avant la première soumission, crédits facturés à la seconde (~30 % moins cher que le cloud), un échec n'est jamais facturé, e-mail quand un job se termine.
- Jobs : deux modes, classique (outils préapprouvés type ffmpeg) ou conteneur (l'entreprise fournit son image Docker et sa commande). Le job doit écrire son fichier de sortie au nom exact indiqué dans le formulaire. Un job trop long est interrompu au délai maximal par tâche.
- Jobs GPU : possibles (image CUDA requise, ex. PyTorch), seulement si un fournisseur avec GPU est en ligne.
- Fournisseur : application Windows, connexion e-mail/mot de passe, Docker Desktop installé automatiquement (validation une seule fois au premier lancement), la machine ne travaille que si disponible et peu sollicitée (CPU < 50 %), paiement au temps de calcul réel (le téléchargement des images n'est pas payé), retraits après vérification d'identité. Comptes particuliers : plafonds de bêta (retraits 100 EUR/30 j, 500 EUR cumulés). Compte société avec SIRET vérifié : pas de plafond.
- Données : hébergées en France, export et suppression du compte possibles depuis la console, fichiers de travail effacés après chaque job.`

// ------------------------------------------------------------------
// Appel Mistral (mode IA). Timeout 25 s, reponse texte brute.

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (bot *ChatBot) askMistral(ctx, userMsg string, history []chatMsg) (string, error) {
	msgs := []chatMsg{{Role: "system", Content: chatSystemPrompt + "\n\nACTIONS DISPONIBLES : resend_verification (renvoyer l'e-mail de vérification), history_email (envoyer le récapitulatif du compte par e-mail).\n\nCONTEXTE UTILISATEUR : " + ctx}}
	for _, h := range history {
		if len(h.Content) > 2000 || len(h.Role) > 20 {
			continue
		}
		if h.Role == "user" || h.Role == "assistant" {
			msgs = append(msgs, chatMsg{Role: h.Role, Content: h.Content})
		}
	}
	msgs = append(msgs, chatMsg{Role: "user", Content: userMsg})

	body, err := json.Marshal(map[string]interface{}{
		"model":       bot.model,
		"messages":    msgs,
		"temperature": 0.3,
		"max_tokens":  400,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", "https://api.mistral.ai/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+bot.apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", fmt.Errorf("mistral HTTP %d : %s", resp.StatusCode, string(b))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("reponse vide")
	}
	return out.Choices[0].Message.Content, nil
}

// ------------------------------------------------------------------
// Handlers HTTP.

// setChatCORS : la vitrine (computeshare.fr) appelle l API sur un
// autre domaine ; POST JSON public sans cookie, origine ouverte.
func setChatCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// POST /chat {message, history?} : reponse {reply, actions, mode}.
// Session OPTIONNELLE : si presente, l assistant personnalise et
// propose des actions.
func (bot *ChatBot) handleChat(w http.ResponseWriter, r *http.Request) {
	setChatCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Message string    `json:"message"`
		History []chatMsg `json:"history"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" || len(msg) > 2000 {
		writeErr(w, http.StatusBadRequest, "message invalide")
		return
	}
	if len(req.History) > 12 {
		req.History = req.History[len(req.History)-12:]
	}

	u := bot.accounts.Authenticate(r)
	limit := 20
	key := "ip:" + clientIP(r)
	if u != nil {
		limit = 60
		key = "u:" + u.ID
	}
	if !bot.allow(key, limit) {
		writeErr(w, http.StatusTooManyRequests, "trop de messages, reessayez dans quelques minutes")
		return
	}

	ctx := bot.userContext(u)
	var actions []map[string]string

	// 1) FAQ prioritaire (rapide, gratuit, maitrise).
	faq := matchFAQ(msg)
	var reply string
	mode := "faq"
	if faq != nil {
		reply = faq.answer
		actions = bot.allowedActions(faq.actions, u)
	} else if bot.apiKey != "" {
		// 2) IA generative pour les questions ouvertes.
		raw, err := bot.askMistral(ctx, msg, req.History)
		if err != nil {
			log.Printf("chat: erreur mistral : %v", err)
			reply = "Je n'ai pas de réponse toute prête à cette question pour le moment. Vous pouvez reformuler, consulter votre console, ou écrire à contact@computeshare.fr pour une aide personnalisée."
			mode = "faq"
		} else {
			mode = "ia"
			reply, actions = parseActionsFromAI(raw, u)
		}
	} else {
		reply = "Je réponds pour le moment aux questions courantes (jobs, tarifs, vérification e-mail, fournisseur, retraits, confidentialité...). Reformulez avec quelques mots-clés, ou écrivez à contact@computeshare.fr pour une demande précise. Astuce : connectez-vous pour un diagnostic personnalisé de votre compte."
		mode = "faq"
	}

	dashLog("chat", "message de "+key+" : "+tail96(msg))
	writeJSON(w, map[string]interface{}{
		"reply":   reply,
		"actions": actions,
		"mode":    mode,
	})
}

// parseActions : extrait les lignes ACTION: <id> finales proposees
// par l IA, les filtre par autorisation, et les retire du texte.
func parseActionsFromAI(raw string, u *User) (string, []map[string]string) {
	lines := strings.Split(raw, "\n")
	var actions []map[string]string
	var kept []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		low := strings.ToLower(t)
		if strings.HasPrefix(low, "action:") {
			id := strings.TrimSpace(t[len("action:"):])
			for _, a := range chatAllowedActions(id, u) {
				actions = append(actions, a)
			}
			continue
		}
		kept = append(kept, ln)
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), actions
}

// catalogue des actions (id -> libelle/description presents a
// l utilisateur ; jamais de detail technique).
func chatActionCatalog(id string) (label, desc string, ok bool) {
	switch id {
	case "resend_verification":
		return "Renvoyer l'e-mail de vérification", "Je vous envoie immédiatement un nouvel e-mail de vérification (lien valable 1 heure).", true
	case "history_email":
		return "Recevoir mon historique par e-mail", "Je vous envoie le récapitulatif de votre compte : jobs, soldes et versements.", true
	}
	return "", "", false
}

func chatAllowedActions(id string, u *User) []map[string]string {
	if u == nil {
		return nil
	}
	if id == "resend_verification" && u.EmailVerified {
		return nil
	}
	label, desc, ok := chatActionCatalog(id)
	if !ok {
		return nil
	}
	return []map[string]string{{"id": id, "label": label, "description": desc}}
}

func (bot *ChatBot) allowedActions(ids []string, u *User) []map[string]string {
	var out []map[string]string
	seen := map[string]bool{}
	for _, id := range ids {
		for _, a := range chatAllowedActions(id, u) {
			if !seen[a["id"]] {
				seen[a["id"]] = true
				out = append(out, a)
			}
		}
	}
	return out
}

func tail96(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 96 {
		return s[:96] + "..."
	}
	return s
}

// POST /chat/action {action_id} : EXECUTION d une action. Session
// OBLIGATOIRE. Toute execution est tracee dans le journal admin.
func (bot *ChatBot) handleChatAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	u := bot.accounts.Authenticate(r)
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "connectez-vous pour utiliser les actions de l assistant")
		return
	}
	var req struct {
		ActionID string `json:"action_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil || req.ActionID == "" {
		writeErr(w, http.StatusBadRequest, "invalid")
		return
	}
	if !bot.allow("act:"+u.ID, 30) {
		writeErr(w, http.StatusTooManyRequests, "trop de demandes, patientez quelques minutes")
		return
	}

	switch req.ActionID {
	case "resend_verification":
		if u.EmailVerified {
			writeJSON(w, map[string]interface{}{"ok": true, "message": "Votre adresse est déjà vérifiée, aucune action nécessaire."})
			return
		}
		if bot.accounts.cooldownRecent("verify_email", u.Email) {
			writeJSON(w, map[string]interface{}{"ok": true, "message": "Un e-mail vient d'être envoyé, patientez une minute et vérifiez votre boîte (et le dossier spam)."})
			return
		}
		raw := bot.accounts.newAuthToken(u.ID, "verify_email", u.Email)
		if bot.emails != nil {
			bot.emails.SendVerification(u.Email, raw)
		}
		dashLog("chat", "renvoi d e-mail de verification pour "+u.Email)
		writeJSON(w, map[string]interface{}{"ok": true, "message": "E-mail de vérification envoyé à " + u.Email + " (lien valable 1 heure). Pensez au dossier spam."})

	case "history_email":
		if bot.emails == nil || !bot.emails.configured() {
			writeErr(w, http.StatusServiceUnavailable, "l envoi d e-mails est momentanement indisponible")
			return
		}
		subject, html := bot.buildHistoryEmail(u)
		if err := bot.emails.send(u.Email, subject, html); err != nil {
			log.Printf("email: historique non envoye a %s : %v", u.Email, err)
			writeErr(w, http.StatusServiceUnavailable, "l envoi a echoue, reessayez dans quelques minutes")
			return
		}
		log.Printf("email: historique envoye a %s", u.Email)
		dashLog("chat", "historique de compte envoye par e-mail a "+u.Email)
		writeJSON(w, map[string]interface{}{"ok": true, "message": "Récapitulatif envoyé à " + u.Email + " : vérifiez votre boîte dans quelques minutes."})

	default:
		writeErr(w, http.StatusBadRequest, "action inconnue")
	}
}

// ------------------------------------------------------------------
// E-mail d historique de compte (action history_email). Genere un
// resume NON technique : jobs et couts cote client, noeuds et gains
// cote fournisseur. Meme style visuel que les autres e-mails.

func (bot *ChatBot) buildHistoryEmail(u *User) (string, string) {
	row := func(label, value string) string {
		return `<tr><td style="padding:6px 12px;border-bottom:1px solid #e2e8f0;color:#64748b">` + label +
			`</td><td style="padding:6px 12px;border-bottom:1px solid #e2e8f0;text-align:right"><b>` + value + `</b></td></tr>`
	}
	var rows string
	if u.Role == "client" && u.OrgID != "" {
		rows += row("Solde de crédits", fmt.Sprintf("%.2f EUR", bot.billing.Balance(u.OrgID)))
		verif := "vérifiée"
		if !u.EmailVerified {
			verif = "NON vérifiée (soumissions bloquées)"
		}
		rows += row("Adresse e-mail", verif)
		rows += row("Statut du compte", u.AccountType)
		jobs := bot.chunker.OrgJobs(u.OrgID, 20)
		n := 0
		for i := len(jobs) - 1; i >= 0; i-- {
			j := jobs[i]
			st, _ := j["chunks"].(map[string]int)
			state := "en cours"
			if st["failed"] > 0 {
				state = fmt.Sprintf("%d réussie(s), %d échouée(s)", st["done"], st["failed"])
			} else if st["queued"]+st["running"] == 0 && st["done"] > 0 {
				state = fmt.Sprintf("%d réussie(s)", st["done"])
			} else if st["done"]+st["failed"]+st["canceled"] == 0 {
				state = "en attente"
			}
			cost, _, _ := bot.billing.JobCost(u.OrgID, fmt.Sprint(j["job_id"]))
			rows += row(fmt.Sprint(j["job_id"]), fmt.Sprintf("%d tâche(s) : %s (%.4f EUR HT)", j["task_count"], state, cost))
			n++
		}
		if n == 0 {
			rows += row("Jobs soumis", "aucun pour le moment")
		}
	} else if u.Role == "seller" {
		nodes := bot.registry.NodesOfUser(u.ID)
		total := 0.0
		for _, nd := range nodes {
			total += bot.wallet.Balance(nd.ID)
		}
		rows += row("Machines fournies", fmt.Sprintf("%d", len(nodes)))
		rows += row("Gains cumulés (toutes machines)", fmt.Sprintf("%.4f EUR", total))
		for _, nd := range nodes {
			state := "hors ligne"
			if nd.Online {
				state = "en ligne"
			}
			gpu := nd.GPUModel
			if gpu == "" {
				gpu = "CPU"
			}
			rows += row(nd.ID, fmt.Sprintf("%s, GPU %s, solde %.4f EUR", state, gpu, bot.wallet.Balance(nd.ID)))
		}
		if len(nodes) == 0 {
			rows += row("Machines", "aucune machine enregistrée")
		}
	}
	if rows == "" {
		rows = row("Compte", "aucune activité enregistrée")
	}
	intro := "Bonjour,<br><br>Voici le récapitulatif de votre compte ComputeShare demandé depuis l'assistant :"
	html := `<!DOCTYPE html><html lang="fr"><body style="font-family:Arial,Helvetica,sans-serif;background:#f8fafc;padding:24px;color:#0f172a">` +
		`<div style="max-width:560px;margin:auto;background:#ffffff;border-radius:12px;padding:32px;border:1px solid #e2e8f0">` +
		`<h2 style="margin-top:0">🥗 ComputeShare</h2>` +
		`<p>` + intro + `</p>` +
		`<table style="width:100%;border-collapse:collapse">` + rows + `</table>` +
		`<p style="color:#64748b;font-size:13px">Historique envoyé à votre demande depuis l'assistant en ligne. Pour toute question : contact@computeshare.fr.</p>` +
		`</div></body></html>`
	return "Votre récapitulatif ComputeShare", html
}
