# Guide client B2B - ComputeShare

Ce guide s'adresse aux entreprises (demandeurs) qui soumettent des
jobs de calcul sur ComputeShare.

## 1. Votre organisation et votre cle API

Votre acces pilote vous a fourni :

- une **cle d'organisation** (format `cshare_...`) : c'est votre
  identifiant de facturation ;
- un **solde de credits prepayes** en euros, visible dans la console.

La cle API est strictement personnelle a votre organisation : toutes
les soumissions, et la facturation, y sont rattachees.

## 2. Soumettre un job

Depuis la console web (https://api.computeshare.fr/me) apres connexion a votre compte :

1. Renseignez votre cle API dans la section Identite.
2. Cliquez sur "Actualiser le solde" : vos credits doivent
   apparaitre. Pas de solde = pas d'execution possible.
3. Renseignez le job :
   - **ID du job** : un identifiant unique de votre choix
   - **Commande** : le programme a executer (ex : `ffmpeg.exe`)
   - **Arguments** : separes par des espaces
   - **Nombre de taches** : en combien de morceaux decouper le travail
   - **Fichier resultat** : le fichier attendu en sortie
4. "Simuler le cout" donne une estimation HT/TTC avant soumission,
   avec l'equivalent AWS pour comparaison.
5. "Soumettre" envoie le job. Les taches sont distribuees aux noeuds
   fournisseurs disponibles.

Chaque tache s'execute dans un **sandbox** isole : pas d'acces
reseau, pas d'acces a vos donnees, arret au bout du timeout.

## 2 bis. Jobs prets a l'emploi (recommande pour demarrer)

La console (https://api.computeshare.fr/me) propose une carte
**"Jobs prets a l'emploi"** : choisissez un modele, televersez vos
donnees, la console prepare le formulaire entier.

Modeles disponibles (valides sur la plateforme) :

- **Agrandir une photo x4 par IA** (GPU, Real-ESRGAN) : votre photo
  + le modele IA (telechargeable depuis la console, 64 Mo, une fois).
- **Convertir une video en MP4** (H.264, compatible partout).
- **Compacter une video** (gain de place).
- **Creer une vignette d'une video** (image a 5 s, parametrable).
- **Extraire l'audio d'une video en MP3**.
- **Agrandir une photo x4** (classique, sans IA, tres bon marche).

Pour les jobs GPU, cochez la case **"Utiliser une carte graphique"**
du formulaire (les modeles IA la cochent automatiquement). Sans cette
case, le job tourne sur CPU : ~100x plus lent pour l'IA.

Les images Docker recettes et les exemples d'appels API sont
detailles dans `docs/DOCKERS-PRETS-A-EMPLOI.md`.

## 3. Suivre et piloter

Le suivi live affiche en temps reel : taches done / running /
queued / failed / canceled, et le cout reel a la duree effective.

- Vous ne payez que la duree reelle d'execution, a la GPU-heure.
- Une tache qui echoue est automatiquement re-planifiee sur un autre
  noeud, sans surcout pour vous.
- La facturation est journalisee ligne par ligne (HT, TVA 20 %,
  total TTC), exploitable en comptabilite.

## 4. Tarification

Les tarifs par categorie de GPU (consumer / prosumer / high_end)
sont publies et geler au moment de la soumission : un job ne sera
jamais refacture a un tarif different a posteriori.

## 5. Limites actuelles (phase pilote)

- Les jobs conteneur (images Docker) sont disponibles ; l'assistance pilote reste a votre cote pour valider votre premiere image.
- Le nombre de noeuds fournisseurs pilotes est volontairement
  limite : certains jobs peuvent etre en file d'attente.
- Pour toute question : votre contact pilote ComputeShare.
