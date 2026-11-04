# Deploiement de production ComputeShare

Ce guide installe l'orchestrateur sur un vrai serveur, avec
PostgreSQL, migrations automatiques et HTTPS automatique.

Le cout de la solution recommandee : environ **4 EUR/mois**
(Hetzner CX22) + un nom de domaine (~10 EUR/an chez OVH).

## Architecture

    Internet -> Caddy (HTTPS Let's Encrypt) -> orchestrateur (port 8081)
                                                     |-> PostgreSQL
                                                     |-> NATS

Tout tourne en conteneurs Docker sur un seul serveur.

## 1. Creer le serveur (Hetzner)

1. Creer un compte sur https://console.hetzner.com (paiement par carte).
2. New Project -> New Server :
   - Location : Falkenstein (Allemagne)
   - Image : Debian 12
   - Type : Shared vCPU -> **CX22** (2 vCPU, 4 Go RAM, 40 Go disque)
   - SSH key : la plus simple pour demarrer = definir un mot de passe
     root (Selectionner "Password" a l'etape SSH keys)
3. Noter l'**adresse IP** du serveur (affichee a la fin).

Pare-feu : a la creation, Hetzner demande un "Firewall". Creer un
firewall avec ces regles entrantes (Inbound rules) :
- TCP 22 (SSH) - depuis n'importe ou
- TCP 80 (HTTP) - depuis n'importe ou
- TCP 443 (HTTPS) - depuis n'importe ou
Rien d'autre. Toutes les regles sortantes restent autorisees.

## 2. Acheter un domaine et le pointer vers le serveur

1. Acheter un domaine (par exemple chez OVH : https://www.ovh.com).
2. Dans la gestion DNS du domaine, creer un enregistrement **A** :
   - Sous-domaine : `api`
   - Cible : l'adresse IP du serveur Hetzner
   - TTL : defaut
3. Attendre quelques minutes. Test depuis son PC :
   `ping api.mondomaine.fr` doit repondre avec l'IP du serveur.

## 3. Installer Docker sur le serveur

Depuis son PC (Windows 10/11 : PowerShell) :

    ssh root@IP_DU_SERVEUR

Puis sur le serveur :

    curl -fsSL https://get.docker.com | sh

## 4. Installer ComputeShare

Toujours sur le serveur :

    git clone https://github.com/pierreh59/computeshare
    cd computeshare/deploy
    cp .env.example .env
    nano .env

Remplir les valeurs :

- `DOMAIN` : le sous-domaine cree a l'etape 2 (ex: `api.mondomaine.fr`)
- `POSTGRES_PASSWORD` : un secret aleatoire. Generer avec :
  `openssl rand -hex 24`
- `ADMIN_KEY` : idem, un autre secret aleatoire (cle des commandes admin)
- `NODE_ENROLLMENT_TOKEN` : idem (jeton que devront fournir les
  noeuds vendeurs pour s'enroler)

Sauver (Ctrl+O, Entree, Ctrl+X dans nano).

Puis lancer la stack :

    docker compose -f docker-compose.prod.yml --env-file .env up -d --build

Premiere fois : quelques minutes (compilation Go + telechargement
des images). Verifier que tout est parti :

    docker compose -f docker-compose.prod.yml ps

Les services doivent etre "running" et "migrate" "exited (0)".

## 5. Verifier

Depuis son PC :

    https://api.mondomaine.fr/nodes

Caddy obtient automatiquement le certificat HTTPS au premier acces
(peut prendre 30 s). La reponse JSON doit apparaitre, et la console
B2B est sur :

    https://api.mondomaine.fr/console

## 6. Brancher l'app desktop sur la production

Dans l'app desktop, champ gateway :

    https://api.mondomaine.fr

Et pour que le noeud passe le controle X-Node-Token, lancer l'app
avec le jeton de production (PowerShell, avant `cargo run`) :

    $env:COMPUTERSHARE_NODE_TOKEN = "le-jeton-choisi-dans-.env"
    cargo run

Ensuite : creer l'organisation cliente et recharger ses credits
depuis le PC, avec la cle admin choisie dans .env :

    $org = Invoke-RestMethod -Method Post https://api.mondomaine.fr/admin/orgs -ContentType "application/json" -Headers @{ "X-Admin-Key" = "la-cle-admin" } -Body '{"name":"ACME Corp"}'
    $org.api_key

## 7. Mises a jour

Sur le serveur :

    cd computeshare
    git pull
    docker compose -f docker-compose.prod.yml --env-file .env up -d --build

Les donnees PostgreSQL sont dans un volume Docker : elles survivent
aux redemarrages et aux mises a jour.

## Notes de securite

- Ne jamais commiter le fichier `.env` (le depot est public).
- Les secrets doivent etre longs et aleatoires (`openssl rand -hex 24`).
- Le serveur n'expose que 80/443 (et 22 pour l'administration SSH).
- Garder l'acces root SSH avec mot de passe uniquement le temps de
  configurer une cle SSH, puis desactiver l'authentification par
  mot de passe (documente dans un futur durcissement).
