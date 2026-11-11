# Dossier juridique ComputeShare - base de travail pour l'avocat

> **Statut de ce document** : preparatoire, redige par l'assistant (pas un
> juriste). Il rassemble les points a cadrer, les textes applicables et des
> propositions de clauses. RIEN ici ne remplace l'avis de l'avocat : chaque
> section doit etre validee/corrigee avant publication ou facturation reelle.
> Date : 11 nov. 2026. Statut societe : EI (Pierre Deg), France.

## 0. Pourquoi ce dossier maintenant

Avant l'ouverture publique (paiements reels, clients externes), il faut
fermer : CGU clients, CGU fournisseurs, conformite RGPD, TVA, fiscalite
des versements aux fournisseurs, statut des fournisseurs (plateforme de
mise en relation), mentions legales. Les paiements en bêta restent
interdits jusqu'a validation (decision actuelle).

## 1. Les deux contrats distincts

ComputeShare est un **intermediaire technique** entre :
- les **clients** (entreprises/particuliers) qui achetent du calcul ;
- les **fournisseurs** (proprietaires de PC) qui louent leur machine.

=> Deux jeux de CGU separes + une politique de confidentialite. Le modele
existe deja (docs/CGU-TEMPLATE.md, sprint 14) et doit etre eclate et
enrichi.

### 1.1 CGU clients (points a cadrer)

- **Objet** : prestation de services d'acces a une plateforme
  d'execution de taches de calcul, AUCUNE garantie de resultat (le job
  peut echouer, il n'est pas facture).
- **Credits prepayes** : achat de credits en euros ; validite a definir
  (illimite en bêta ? 12 mois ?) ; remboursement en cas de fermeture
  (formule a cadrer, ex. solde > 10 EUR remboursable sur demande 30 j).
- **Prix** : tarifs publiques par categorie, gels au moment de la
  soumission (deja implemente : pricing ADR-006), prix HT + TVA.
- **Obligations du client** : ne soumettre que des donnees dont il detient
  les droits ; ne pas soumettre de contenus illegaux ; ne pas tenter
  d'extraire des informations sur les fournisseurs ; respecter les lois
  sur l'export (donnees chiffrees non exportables hors UE ? a definir).
- **Acceptation des risques du modele distribue** : les donnees d'entree
  transitent et sont executees sur des machines de tiers ; recommandation
  forte de chiffrer/neutraliser les donnees sensibles ; la plateforme
  applique des protections techniques (conteneur sans reseau, effacement
  apres job, controle d'acces aux fichiers) MAIS la protection
  contractuelle passe par les CGU fournisseurs (clause de confidentialite).
- **Facturation** : a la duree reelle, journal d'evenements exploitable,
  facture mensuelle ou sur demande ; TVA selon profil (voir 4).
- **Limitation de responsabilite** : plafond a definir (ex. montant
  depense sur les 12 derniers mois) ; exclusions classiques.
- ** Suspension** : cloture possible pour usage abusif (anti-fraude
  existant : blocage, re-scheduling).

### 1.2 CGU fournisseurs (points a cadrer) - le chantier le plus sensible

**Statut** : le fournisseur n'est NI salarie NI mandaté de ComputeShare.
La qualification de reference est la **presomption de non-salariat des
travailleurs de plateformes de mise en relation** (art. L7341-1 du Code
du travail, art. 44 loi "mobilités" 2019-1806) : presumé non lie par un
contrat de travail celui qui met a disposition un bien/service via une
plateforme de mise en relation. Pour que cette presomption tienne, les
CGU doivent refléter les critères de la jurisprudence (pas de lien de
subordination) :

- **liberte totale de refuser un travail** (l'app a un bouton pause,
  seuils CPU/GPU, aucun minimum de volume : decrire ces libertes
  explicitement dans les CGU) ;
- **aucune exclusivite** ni interdiction de travailer pour un concurrent ;
- **aucun horaire impose** ;
- pas de directive continue sur LA MANIERE de travailer ;
- versement par tache reussie, pas de remuneration minimale garantie ;
- pas de sanction autre que l'acces au service (blocage en cas de fraude).

Clause express : "Le fournisseur agit en toute independance ; la
relation ne cree aucun lien de subordination, de salariat, de
mandat ni d'exclusivite." + clause de requalification (si un
juge requalifie, responsabilités a cadrer).

**Clauses specifiques fournisseur** :
- exigences techniques (config minimale, antivirus a jour, machine sous
  son controle physique : pas sur un poste d'entreprise sans accord de
  l'employeur) ;
- interdiction d'exfiltrer les donnees executees (lecture des fichiers
  interdite, copie interdite) ; obligation d'effacement (automatique,
  deja implemente sprint 27c) ;
- KYC : identite verifiee avant retraits (deja : e-mail verifie, comptes
  societe + SIRET INSEE ; prevoir piece d'identite avant premier
  versement reel — seuil a definir avec l'avocat, cf. obligations LCB-FT
  ci-dessous) ;
- plafonds particuliers deja implementes (100 EUR/30 j, 500 EUR cumules,
  societe verifiee sans plafond) : a documenter dans les CGU ;
- clause de desactivation : la plateforme peut suspendre/bloquer un
  nœud (fraude, lenteur anormale, instabilite) avec journal d'alertes.

## 2. RGPD et donnees

- **Base legale des traitements** : contrat (comptes, facturation),
  interet legitime (anti-fraude, securite), consentement (horodate,
  deja implemente).
- **Registre des traitements** (art. 30) : A REDIGER (fiche par
  finalite : comptes, jobs, facturation, anti-fraude, geoloc IP).
- **Donnees personnelles manipulees** : e-mail, nom, mot de passe (hash
  bcrypt), IP des nœuds (memoire seule, non persistee), adresse
  societe, SIRET, historique de gains, evenements anti-fraude.
- **Sous-traitants** (contrats art. 28 a avoir) : OVH (hebergement),
  Brevo (e-mails transactionnels), INSEE (verification SIRET = donnees
  publiques d'entreprises, pas RGPD-personnes physiques), Google
  (updater) — cartes de la liste a tenir a jour.
- **Droits** : portabilite/export (fait), suppression/anonymisation
  (fait, sprint 15c), mot de passe + sessions (fait), doublons de
  demandes a prevoir (formulaire de contact).
- **Durees de conservation** : a fixer (comptes supprimes = ecritures
  comptables 10 ans, logs anti-fraude 12 mois, etc.).
- **Point sensible** : les DONNEES CLIENTS executees sur les machines
  fournisseurs. Position a formaliser : ComputeShare est "processeur de
  moyens" pas processeur du contenu ; obligation de moyens (isolation
  conteneur, sans reseau, effacement) ; recommandation aux clients de
  chiffrer. A cadrer : est-ce que le client reste "responsable de
  traitement" de ses donnees et ComputeShare un simple intermediaire
  technique ? (analyses a faire valider).
- **Geoloc des nœuds** (dashboard admin, ip-api) : memoire seulement,
  but securite/anti-fraude, non stockee : a inscrire au registre.
- **Clause DPA** entre ComputeShare et clients qui soumettent des
  donnees personnelles (encadrement : le client reste responsable du
  traitement ; ComputeShare ne stocke pas les contenus au-dela du
  rapatriement des resultats).

## 3. Fiscalite des versements aux fournisseurs (242 bis CGI)

- La plateforme est **declarant tiers** (art. 242 bis du CGI) : pour
  tout fournisseur qui encaisse plus de 50 EUR/annee via la plateforme
  (seuil declaratif), ComputeShare doit declarer a l'administration
  fiscale les revenus bruts de l'annee + envoyer une attestation au
  fournisseur (recapitulatif annuel avant le 31 janvier, remise en
  place de la 2e annee).
- Depuis le 1er oct. 2024, la dispense de declaration pour les revenus
  < 305 EUR a ete SUPPRIMEE : les plateformes declarent des 50 EUR/an.
- Cote fournisseur : gains imposables (BIC/IC pour particuliers) ; la
  plateforme doit fournir une attestation fiscale annuelle (idem
  Uber/Airbnb/Leboncoin).
- A implementer avant les paiements reels : module d'attestation
  annuelle (export des gains par fournisseur par annee) + champ
  "revenus bruts" exacts.

## 4. TVA

- **Clients entreprises UE** : services B2B automatiquement soumis a la
  TVA du pays du client (autoliquidation par le client, art. 44/196
  directive TVA). ComputeShare facture HT si le client fournit un
  numero de TVA intracommunautaire valide (verification VIES - champ
  deja present, verification a activer).
- **Clients particuliers FR** : TVA francaise 20 % (deja facturee).
- **Clients particuliers UE** : TVA du pays du client a partir du
  seuil (10 000 EUR/an, option registre OSS a etudier plus tard).
- Actuellement la plateforme applique 20 % a tous : CORRECTIF
  NECESSAIRE (methode de facturation par profil : societe + TVA UE
  validee = autoliquidation, ligne "TVA : autoliquee par le client").
- Seuil de franchise en base EI : 36 800 EUR services (FR) : sous ce
  seuil, TVA non applicable aux ventes FR mais MUST facturer "TVA non
  applicable, art. 293 B du CGI". A cadrer avec le comptable ET
  verifier le regime micro-BIC (services BIC 77 700 EUR ; CA
  ComputeShare = prestations de services).

## 5. LCB-FT / KYC (obligations monnaies et instruments de laloi)

- Versements aux fournisseurs = "prestations de services" ; les
  plateformes de paiement/echange sont visees par les obligations
  LCB-FT quand elles executent des paiements. Si ComputeShare paie via
  son propre wallet (virements SEPA), vigilance sur les obligations
  Declare/verif en fonction des montants.
- Minima prudent : identite verifiee (e-mail + piece d'identite au-dela
  d'un seuil, ex. 100 EUR/an), plafonds particuliers (deja
  implementes), blocage des retraits suspects, conservations des
  justificatifs 5 ans.
- Stripe Connect (quand mis en place) delegue une partie du KYC.

## 6. Mentions legales & site (LCEN)

- Mentions obligatoires (art. 6, loi 2004-575) : editeur (EI Pierre
  Deg), SIRET, adresse, directeur de publication, hebergeur (OVH,
  Gravelines), contact. A ajouter sur computeshare.fr (page
  "Mentions legales").
- CGU + politique de confidentialite : liens au moment de
  l'inscription (deja : case consentement + page /privacy).
- Cookies : la plateforme n'utilise pas de cookies de suivi ;
  session localStorage uniquement (a documenter).

## 7. DSA / autres

- ComputeShare = plateforme de mise en relation B2B : obligations DSA
  allégées (transparence, pas de marketplace grand public pour
  l'instant). A re-evaluer si ouverture B2C large.
- Droit de la concurrence / plateformes : rien de bloquant a ce stade.
- Propriete intellectuelle : les resultats appartiennent au client ;
  la plateforme n'acquiert aucun droit sur les donnees executees.
  Clause a ecrire noir sur blanc dans les CGU clients.

## 8. Checklist avant ouverture publique

1. [ ] CGU clients finalisees + affichees + case consentement
2. [ ] CGU fournisseurs finalisees (statut non-salarie, libertes,
       confidentialite des donnees)
3. [ ] Politique de confidentialite RGPD + registre des traitements
4. [ ] Methode TVA par profil (B2B UE autoliquidation) implementee
5. [ ] Mentions legales publiees sur la vitrine
6. [ ] Attestation fiscale annuelle fournisseurs (242 bis) :
       specifiee, module prevu
7. [ ] KYC retraits (piece d'identite au seuil) : specifie
8. [ ] DPA modele (clients avec donnees personnelles)
9. [ ] Comptable : validation regime EI/micro-BIC + TVA
10. [ ] Avocat : validation de l'ensemble

## 9. Rappels importants pour l'avocat (contexte produit)

- Le wallet fournisseur plafonne les particuliers (100 EUR/30 j,
  500 EUR cumules) et les societes non verifiees.
- La verification SIRET (INSEE) et le champ TVA intracommunautaire
  existent deja ; VIES non branchee.
- Les e-mails transactionnels (Brevo) : finalites = verification
  compte, mot de passe, notifications job/credits, bilan hebdo
  administrateur.
- L'anti-fraude existant : Sybil/reconnexion, lenteur, ban admin
  (persiste, redistribue les chunks), throttle login/signup.
- Les fichiers clients sont effaces du PC fournisseur apres le job ;
  le conteneur n'a pas d'acces reseau ; seul /work est inscriptible.
- La plateforme ne stocke PAS les images Docker (registres externes),
  ni le contenu des assets au-dela du rapatriement (dureree du stockage
  des resultats sur le serveur : a limiter contractuellement, ex. 30 j).
