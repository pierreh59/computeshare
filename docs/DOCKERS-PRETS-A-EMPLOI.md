# Dockers prets a l'emploi (images validees sur la plateforme)

Ce document liste les images Docker **testees reellement** sur
ComputeShare (jobs conteneur), avec la commande exacte a utiliser.
Objectif : un client ne bricole rien, il prend une recette validee.

Derniere validation : 11 nov. 2026 (sprint 34).

## Comment ca marche

- Un job conteneur = une image Docker + une commande + des arguments.
- Vos fichiers d'entree sont injectes dans `/work` du conteneur
  (upload dans la console ou API `/v1/assets`), resultat attendu dans
  `/work` aussi (champ "fichier resultat").
- Les conteneurs tournent SANS reseau, lectures seules sauf /work,
  2 vCPU / 2 Go RAM, timeout par tache defini par le client.
- La console client (https://api.computeshare.fr/me) propose
  directement des **jobs prets a l'emploi** bases sur ces recettes
  (carte "Jobs prets a l'emploi") : 2 clics, aucun reglage.

## 1. Images maison (simples, universelles)

| Image | Usage | Taille |
|---|---|---|
| `alpine:latest` | scripts shell, traitement texte, tout-trivial | 7 Mo |
| `ubuntu:24.04` | scripts necessitant des outils GNU | 78 Mo |

Exemple (concatener un fichier) :
```json
{ "id": "mon-job", "runtime": "container", "image": "alpine:latest",
  "command": "sh", "args": ["-c", "cat entree.txt > sortie.txt"],
  "task_count": 1, "timeout_secs": 600, "output_name": "sortie.txt" }
```

## 2. ffmpeg (video et audio) - image `jrottenberg/ffmpeg:6-alpine`

Validee le 11 nov. 2026 sur la plateforme (generation, encodage,
extraction audio, vignette, agrandissement photo). Cette image a un
ENTRYPOINT ffmpeg : la "commande" du job est le PREMIER argument de
la ligne ffmpeg (pas le mot ffmpeg).

Recettes validees (fichier d'entree = ce que vous uploadez, remplacer
"mavideo.mp4" par votre nom de fichier) :

- **Encoder en MP4 H.264/AAC** : commande `-i`, args
  `["mavideo.mp4","-c:v","libx264","-preset","medium","-crf","23","-pix_fmt","yuv420p","-c:a","aac","-b:a","128k","video.mp4"]`,
  resultat `video.mp4`.
- **Compacter une video** : meme chose avec `-preset fast -crf 28`
  et audio 96k, resultat `video-petite.mp4`.
- **Extraire l'audio en MP3** (libmp3lame present, teste) :
  commande `-i`, args
  `["mavideo.mp4","-vn","-c:a","libmp3lame","-q:a","2","audio.mp3"]`,
  resultat `audio.mp3`.
- **Vignette d'une video** : commande `-ss`, args
  `["00:00:05","-i","mavideo.mp4","-frames:v","1","vignette.png"]`
  (5 secondes dans la video ; adapter si video plus courte),
  resultat `vignette.png`.
- **Agrandir une photo x4 (Lanczos, sans IA)** : commande `-i`,
  args `["photo.png","-vf","scale=iw*4:ih*4:flags=lanczos","photo-x4.png"]`,
  resultat `photo-x4.png`.

## 3. IA : PyTorch + CUDA - image `pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime`

Validee le 9-11 nov. 2026 (upscale Real-ESRGAN, burn GPU 20 GFLOPS
sur RTX 5070, traitement par bandes de 5016px). IMPORTANT : les cartes
RTX 50xx (Blackwell) exigent CUDA 12.8+, donc cette image.

- Job GPU : cocher "Utiliser une carte graphique" dans la console
  (ou `"gpu": true` dans l'API).
- Modele d'upscale photo (Real-ESRGAN x4) : le modele
  `RealESRGAN_x4.pth` (67 Mo) se telecharge ici :
  https://huggingface.co/ai-forever/Real-ESRGAN/resolve/main/RealESRGAN_x4.pth
  (aucun renommage necessaire, sprint 35 : la console pointe
  directement ce lien). Il est televerse comme
  fichier d'entree, une fois ; il reste utilisable pour tous vos jobs.
- La console propose le modele "Agrandir une photo x4 (IA, GPU)" qui
  emballe tout : script PyTorch complet, traitement par tuiles
  (grandes photos OK), verification du modele, gestion BGR.
- Sans GPU (CPU), le meme job marche mais ~100x plus lent : reservez
  le aux petites images ou cochez GPU.

## 4. A eviter pour l'instant

- Les outils bases sur **Vulkan** (ncnn-vulkan, ex.
  realesrgan-ncnn-vulkan) : WSL2 n'expose pas Vulkan NVIDIA ; ils
  retombent en CPU et explosent le timeout. Preferez les versions
  PyTorch/CUDA.
- Les jobs qui ont besoin d'INTERNET pendant l'execution (pip
  install, telechargement de modeles) : les conteneurs tournent sans
  reseau par securite. Tout ce qui est necessaire doit etre dans
  l'image ou televerse en fichier d'entree.

## 5. Fabriquer votre propre image (avance)

Les jobs de votre entreprise peuvent embarquer vos outils et vos
modeles dans une image dediee (plus besoin d'uploader le modele a
chaque job) :

```dockerfile
FROM pytorch/pytorch:2.7.1-cuda12.8-cudnn9-runtime
WORKDIR /work
COPY mon-modele.pth /opt/modeles/mon-modele.pth
COPY mon_script.py /opt/outils/mon_script.py
```

Construisez et publiez l'image sur un registre public (Docker Hub,
GHCR) puis referencez-la dans le champ "image" du job. La plateforme
ne stocke pas les images : les noeuds les telechargent directement
depuis le registre (le premier pull est facture 20 pourcent du tarif,
ensuite l'image reste en cache chez le fournisseur).

## Recette pour tester votre image avant de payer

Sur n'importe quelle machine avec Docker, repliquez le contexte exact
de la plateforme avant de soumettre :

```
docker run --rm --memory 2g --cpus 2 --network none --cap-drop ALL \
  --security-opt no-new-privileges --pids-limit 256 --read-only \
  --tmpfs /tmp:rw,size=64m -v $(pwd)/dossier-test:/work -w /work \
  votre-image votre-commande vos-arguments
```

Si ca marche la, ca marchera sur ComputeShare.
