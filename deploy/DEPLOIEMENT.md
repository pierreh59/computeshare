# Deploiement de production ComputeShare (OVHcloud)

Ce guide installe l'orchestrateur sur un VPS OVHcloud, avec
PostgreSQL, migrations automatiques et HTTPS automatique.

Solution retenue : **VPS OVHcloud VPS-1 2027** (2 vCores, 4 Go RAM,
40 Go NVMe, sauvegarde quotidienne incluse) - environ 4,50 EUR
HT/mois sans engagement. Donnees hebergees en France (argument
RGPD pour les clients B2B), trafic illimite, meme compte OVH que
le nom de domaine.

## Architecture

    Internet -> Caddy (HTTPS Let's Encrypt) -> orchestrateur (port 8081)
                                                     |-> PostgreSQL
                                                     |-> NATS

Tout tourne en conteneurs Docker sur un seul serveur.

## 1. Commander le VPS (OVHcloud, meme compte que le domaine)

1. Dans votre compte OVHcloud, rubrique **VPS** -> offre
   **VPS-1 2027** (2 vCores / 4 Go / 40 Go NVMe).
   Si l'offre est momentanement indisponible : reessayer avec
   une autre localisation, patienter quelques jours (OVH
   reapprovisionne regulierement), ou prendre le VPS-2 2027
   sans engagement le temps de la phase pilote.
2. **Localisation** : un datacenter en France (Gravelines ou
   Strasbourg).
3. **Systeme** : **Debian 12**, sans panel ni application
   preinstallee (refuser Plesk, cPanel, "Apps", Windows).
4. **Duree** : mensuel **sans engagement** (liberte de changer
   de palier pendant la beta).
5. **Authentification root** : definir un mot de passe long et
   le noter precieusement.
6. Une fois livre, relever l'**adresse IPv4** du serveur dans
   le Manager OVH (Bare Metal Cloud -> VPS -> votre VPS).

## 2. Brancher le domaine (zone DNS OVH)

Le domaine computeshare.fr est deja gere par OVH. Dans le
Manager : **Web Cloud -> computeshare.fr -> Zone DNS**, ajouter
deux enregistrements **A** pointant vers l'IPv4 du VPS :

- sous-domaine vide (racine) : pour le site vitrine
  (computeshare.fr)
- sous-domaine `api` : pour la plateforme
  (api.computeshare.fr)

Si OVH propose de modifier les serveurs DNS : ne rien changer,
garder les DNS OVH par defaut. Propagation : quelques minutes
a une heure.

## 3. Se connecter au serveur et installer Docker

Depuis son PC (Windows 10/11 : PowerShell) :

    ssh root@IP_DU_SERVEUR

(repondre "yes" a la question sur la fingerprint, puis saisir
le mot de passe root defini a la commande).

Sur le serveur :

    curl -fsSL https://get.docker.com | sh

Pare-feu du serveur (n'exposer que l'essentiel) :

    apt-get update && apt-get install -y ufw
    ufw allow 22/tcp
    ufw allow 80/tcp
    ufw allow 443/tcp
    ufw --force enable
    ufw status

## 4. Installer ComputeShare

Toujours sur le serveur :

    apt-get install -y git
    git clone https://github.com/pierreh59/computeshare
    cd computeshare/deploy
    cp .env.example .env
    nano .env

Remplir les valeurs :

- `DOMAIN` : `api.computeshare.fr`
- `SITE_DOMAIN` : `computeshare.fr`
- `POSTGRES_PASSWORD` : secret aleatoire, generer avec
  `openssl rand -hex 24`
- `ADMIN_KEY` : autre secret aleatoire (cle des commandes
  admin /admin/*)
- `NODE_ENROLLMENT_TOKEN` : autre secret aleatoire (jeton que
  doivent fournir les noeuds vendeurs pour s'enroler)

Sauver (Ctrl+O, Entree, Ctrl+X dans nano), puis lancer la
stack :

    docker compose -f docker-compose.prod.yml --env-file .env up -d --build

Premiere fois : quelques minutes (compilation Go + images).
Verifier que tout est parti :

    docker compose -f docker-compose.prod.yml ps

Les services doivent etre "running" et "migrate" "exited (0)".

## 5. Verifier

Depuis son PC :

- https://computeshare.fr -> le site vitrine
- https://api.computeshare.fr/nodes -> reponse JSON de l'API
- https://api.computeshare.fr/console -> console client B2B

Caddy obtient automatiquement le certificat HTTPS au premier
acces (peut prendre 30 s).

## 6. Brancher l'app desktop sur la production

Dans l'app desktop, champ gateway :

    https://api.computeshare.fr

Et pour que le noeud passe le controle X-Node-Token, lancer
l'app avec le jeton de production (PowerShell, avant
`cargo run`) :

    $env:COMPUTERSHARE_NODE_TOKEN = "le-jeton-choisi-dans-.env"
    cargo run

Ensuite : creer l'organisation cliente et recharger ses credits
depuis le PC, avec la cle admin choisie dans .env :

    $org = Invoke-RestMethod -Method Post https://api.computeshare.fr/admin/orgs -ContentType "application/json" -Headers @{ "X-Admin-Key" = "la-cle-admin" } -Body '{"name":"ACME Corp"}'
    $org.api_key

## 7. Mises a jour

Sur le serveur :

    cd computeshare
    git pull
    docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env up -d --build

Les donnees PostgreSQL sont dans un volume Docker : elles
survivent aux redemarrages et aux mises a jour. La sauvegarde
quotidienne OVH (24 h glissantes) couvre les maladresses
recentes ; une sauvegarde externe planifiee sera ajoutee avant
l'ouverture de la beta.

## Notes de securite

- Ne jamais commiter le fichier `.env` (le depot est public).
- Les secrets doivent etre longs et aleatoires (`openssl rand -hex 24`).
- Le serveur n'expose que 22/80/443 (ufw).
- Garder l'acces root par mot de passe le temps de configurer
  une cle SSH, puis desactiver l'authentification par mot de
  passe dans /etc/ssh/sshd_config (PasswordAuthentication no)
  et relancer le service ssh.
