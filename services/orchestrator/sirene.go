package main

// Sprint 15d (LAI-61) - Verification automatique SIRET via l'API INSEE
// SIRENE V3 (https://api.insee.fr). Un compte "societe" est verifie a
// l'inscription et a chaque modification de SIRET.
//
// Marche en mode degrades :
//   - sans identifiants INSEE (env INSEE_KEY/INSEE_SECRET vides) :
//     le SIRET reste controle localement (cle Luhn) mais le statut
//     reste "non verifie" ; la console admin peut forcer la
//     verification manuelle plus tard (ticket avocat).
//   - API injoignable / erreur : idem, aucune inscription bloquee.

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// sireneClient : jeton OAuth2 INSEE mis en cache (validite ~7 jours,
// on le rafraichit toutes les 6 h par securite).
type sireneClient struct {
	token   string
	expires time.Time
}

var sirene = &sireneClient{}

func sireneCreds() (string, string, bool) {
	k := strings.TrimSpace(os.Getenv("INSEE_KEY"))
	s := strings.TrimSpace(os.Getenv("INSEE_SECRET"))
	return k, s, k != "" && s != ""
}

// sireneToken obtient (et cache) un jeton OAuth2 client_credentials.
func (c *sireneClient) getToken() (string, bool) {
	key, secret, ok := sireneCreds()
	if !ok {
		return "", false
	}
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, true
	}
	req, err := http.NewRequest("POST", "https://api.insee.fr/token",
		strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", false
	}
	req.SetBasicAuth(key, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&out) != nil || out.AccessToken == "" {
		return "", false
	}
	c.token = out.AccessToken
	// marge : on rafraichit avant l'expiration reelle
	if out.ExpiresIn > 3600 {
		c.expires = time.Now().Add(time.Duration(out.ExpiresIn-3600) * time.Second)
	} else {
		c.expires = time.Now().Add(time.Duration(out.ExpiresIn/2) * time.Second)
	}
	return c.token, true
}

// sireneResult : synthese d'une verification d'etablissement.
type sireneResult struct {
	Verified bool
	Name     string // raison sociale declaree a l'INSEE
	Address  string // adresse de l'etablissement siege
	Active   bool   // etablissement en activite
}

// checkSiret interroge l'API SIRENE V3. Retourne nil si la
// verification n'a pas pu etre effectuee (pas d'identifiants, erreur
// reseau, SIRET inconnu = etablissement ferme ou inexistant).
func (c *sireneClient) checkSiret(siret string) *sireneResult {
	tok, ok := c.getToken()
	if !ok {
		return nil
	}
	req, err := http.NewRequest("GET",
		"https://api.insee.fr/entreprises/sirene/V3/siret/"+siret, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		return nil
	}
	defer resp.Body.Close()
	var out struct {
		Etablissement struct {
			AdresseEtablissement struct {
				NumVoie    string `json:"numeroVoieEtablissement"`
				TypeVoie   string `json:"typeVoieEtablissement"`
				LibelleVoie string `json:"libelleVoieEtablissement"`
				CodePostal string `json:"codePostalEtablissement"`
				Commune    string `json:"libelleCommuneEtablissement"`
			} `json:"adresseEtablissement"`
			UniteLegale struct {
				Denomination string `json:"denominationUniteLegale"`
				Nom          string `json:"nomUniteLegale"`
				Prenom       string `json:"prenom1UniteLegale"`
				Etat         struct {
					Active bool `json:"actifV3"`
				} `json:"etatAdministratifUniteLegale"`
			} `json:"uniteLegale"`
		} `json:"etablissement"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil
	}
	e := out.Etablissement
	name := e.UniteLegale.Denomination
	if name == "" {
		// entreprise individuelle : nom + prenom de la personne physique
		name = strings.TrimSpace(e.UniteLegale.Prenom + " " + e.UniteLegale.Nom)
	}
	addr := strings.TrimSpace(strings.Join([]string{
		e.AdresseEtablissement.NumVoie, e.AdresseEtablissement.TypeVoie,
		e.AdresseEtablissement.LibelleVoie,
	}, " "))
	if e.AdresseEtablissement.CodePostal != "" {
		addr += ", " + e.AdresseEtablissement.CodePostal + " " + e.AdresseEtablissement.Commune
	}
	return &sireneResult{
		Verified: true,
		Name:     name,
		Address:  addr,
		Active:   e.UniteLegale.Etat.Active,
	}
}

// verifySiret : verification d'un compte utilisateur + persistance.
// Appelle l'API SIRENE, met a jour le compte et le store. Si l'API
// n'est pas configuree ou indisponible, le compte reste "non verifie"
// (statut consultable, aucune inscription bloquee).
func (a *AccountStore) verifySiret(userID string) {
	a.mu.RLock()
	u, ok := a.users[userID]
	siret := u.Siret
	a.mu.RUnlock()
	if !ok || siret == "" || !validSiret(siret) {
		return
	}
	res := sirene.checkSiret(siret)
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	u, ok = a.users[userID]
	if !ok {
		return
	}
	u.SiretCheckedAt = &now
	if res != nil && res.Active {
		u.SiretVerified = true
		// Les donnees officielles INSEE font foi : on complete le nom
		// et l'adresse si le compte ne les avait pas renseignes.
		if u.CompanyName == "" {
			u.CompanyName = res.Name
		}
		if u.CompanyAddress == "" {
			u.CompanyAddress = res.Address
		}
		log.Printf("sirene: SIRET %s de %s verifie (%s)", siret, userID, res.Name)
	} else {
		u.SiretVerified = false
		if res != nil {
			log.Printf("sirene: SIRET %s de %s INACTIF ou refuse", siret, userID)
		} else {
			log.Printf("sirene: verification SIRET %s non effectuee (API absente/erreur)", siret)
		}
	}
	a.store.SaveUserCompany(u)
}
