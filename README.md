# Spin the Wheel

Roue multi-utilisateurs, éditable en ligne et intégrable en
iframe dans Digipad.

- **Backend** : Go, sans framework HTTP, PostgreSQL.
- **Front** : HTML/CSS/JS vanilla, modules ES, roue peinte sur un canvas 2D.
- **Authentification** : Google OAuth 2.0 + PKCE, liste blanche d'adresses.
- **Déploiement** : Docker Compose, HTTP en clair derrière un nginx qui fait le TLS.
- **Identifiants** : UUIDv7 partout, l'horodatage de création est porté par la clé primaire.

Le site public est `https://wheel.nicolas-legay.fr`.

---

## 1. Ce que fait l'application

Deux surfaces, volontairement séparées :

| | Édition (`/`, `/wheels/{id}`, `/comptes`) | Intégration (`/embed/{id}`) |
|---|---|---|
| Accès | session Google obligatoire | aucun compte requis |
| Actions | créer, modifier, supprimer, téléverser | afficher et faire tourner |
| Iframe | interdite (`frame-ancestors 'none'`) | autorisée pour Digipad uniquement |

Une roue porte un titre et de 2 à 64 segments. Un segment est fait d'une couleur
de fond, d'une image et d'un libellé. L'image est l'élément principal : elle
occupe tout l'espace du quartier. Le libellé est facultatif et ne s'affiche que
s'il est rempli. Les deux peuvent être omis : le segment est alors un simple
aplat de couleur, annoncé par son rang au tirage.

À l'arrêt de la roue, le résultat s'affiche en grand par-dessus, avec l'image du
segment gagnant et une salve de confettis.

---

## 2. Prérequis

- Docker et Docker Compose v2
- Un nom de domaine pointant sur le serveur, avec TLS géré par nginx
- Un projet Google Cloud pour l'identifiant OAuth

---

## 3. Créer l'identifiant Google OAuth

1. Ouvrir <https://console.cloud.google.com/> et sélectionner (ou créer) un projet.
2. **API et services → Écran de consentement OAuth**
   - Type : **Externe** (ou **Interne** si Google Workspace).
   - Renseigner nom de l'application, e-mail d'assistance, e-mail du développeur.
   - Champs d'application : `openid`, `.../auth/userinfo.email`, `.../auth/userinfo.profile`.
3. **API et services → Identifiants → Créer des identifiants → ID client OAuth**
   - Type d'application : **Application Web**
   - Origine JavaScript autorisée : `https://wheel.nicolas-legay.fr`
   - **URI de redirection autorisé** : `https://wheel.nicolas-legay.fr/auth/google/callback`
4. Copier l'**ID client** et le **code secret** dans le fichier `.env`.

L'URI de redirection doit correspondre au caractère près, sinon Google renvoie
`redirect_uri_mismatch`.

---

## 4. Démarrage

```bash
cp .env.example .env
```

Générer les trois secrets et les coller dans `.env` :

```bash
openssl rand -hex 32
```

- `POSTGRES_PASSWORD` — mot de passe de la base
- `IP_HASH_SALT` — sel de pseudonymisation des adresses IP

Renseigner aussi `BASE_URL`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` et
`BOOTSTRAP_ADMIN_EMAILS` (votre adresse, sans quoi personne ne pourra se
connecter au premier démarrage).

Puis :

```bash
docker compose up -d --build
```

Le serveur écoute sur `127.0.0.1:8080`. Les migrations SQL s'appliquent au
démarrage, et les adresses de `BOOTSTRAP_ADMIN_EMAILS` sont insérées dans la
liste blanche.

Vérifier :

```bash
curl -s localhost:8080/healthz
```

---

## 5. Configuration nginx

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name wheel.nicolas-legay.fr;

    ssl_certificate     /etc/letsencrypt/live/wheel.nicolas-legay.fr/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/wheel.nicolas-legay.fr/privkey.pem;

    # Marge au-dessus de MAX_IMAGE_BYTES pour l'enveloppe multipart.
    client_max_body_size 4m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;

        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_read_timeout 65s;
    }
}
```

**Deux pièges à éviter :**

1. Ne pas ajouter `add_header X-Frame-Options ...` ni
   `add_header Content-Security-Policy ...` dans ce `server`. L'application
   pose déjà ces en-têtes, différemment selon la page ; un en-tête global
   casserait l'intégration Digipad.
2. `TRUST_PROXY=true` suppose que l'application n'est **joignable que via
   nginx**. Le port est publié sur `127.0.0.1` uniquement, ce qui garantit ce
   point. Si vous l'exposez autrement, passez `TRUST_PROXY=false` — sinon
   n'importe qui peut usurper son adresse IP et contourner la limitation de
   débit.

---

## 6. Utilisation

1. Se connecter sur `https://wheel.nicolas-legay.fr` avec Google.
2. Créer une roue, ajouter les segments (image, couleur, libellé facultatif).
3. Cocher **Roue publiée**, puis **Enregistrer**.
4. Copier le code iframe et le coller dans une vignette Digipad :

```html
<iframe src="https://wheel.nicolas-legay.fr/embed/VOTRE-UUID"
        width="480" height="640" style="border:0;max-width:100%"
        title="Spin the Wheel" loading="lazy"></iframe>
```

Décocher **Roue publiée** suffit à couper l'accès à l'iframe sans supprimer la roue.

Pour autoriser un autre domaine que Digipad, modifier `EMBED_FRAME_ANCESTORS`
dans `.env` puis `docker compose up -d`.

---

## 7. Modèle de sécurité

**Ce contre quoi on se protège, et comment.**

| Menace | Réponse |
|---|---|
| XSS | CSP `default-src 'none'` sans `unsafe-inline` ; aucun script ni style en ligne ; le front n'utilise que `textContent` et `createElement`, jamais `innerHTML` ; les libellés sont peints sur un canvas |
| XSS par image | toute image est décodée puis **ré-encodée en PNG** par le serveur ; SVG refusé ; `Content-Type` imposé + `nosniff` |
| CSRF | cookie `SameSite=Lax` + jeton `X-CSRF-Token` par session + contrôle `Sec-Fetch-Site`/`Origin` sur toute requête modifiante |
| Vol de session | cookie `__Host-`, `HttpOnly`, `Secure` ; seul le SHA-256 du jeton est stocké ; révocation immédiate au retrait de la liste blanche |
| Clickjacking | `frame-ancestors 'none'` sur les pages d'édition ; liste fermée d'origines sur `/embed` ; joker global refusé au démarrage |
| Connexion forcée (login CSRF) | `state` OAuth à usage unique en base **et** lié au navigateur par un cookie éphémère ; PKCE S256 |
| Injection SQL | requêtes exclusivement paramétrées ; couleur contrainte par `CHECK` jusque dans le schéma |
| Traversée de répertoire | les noms de fichiers dérivent d'UUID déjà analysés, jamais d'une chaîne du client |
| Tirage truqué | le résultat est tiré côté serveur avec `crypto/rand` ; le client ne fait qu'animer |
| Déni de service | seaux à jetons par IP, par roue et par compte ; bornes de taille sur les corps, les images et le nombre de segments ; plafond horaire par roue |
| Bombe de décompression | dimensions vérifiées **avant** décodage complet (2048 px maximum par côté) |
| Élévation via une autre roue | le propriétaire est filtré dans la requête SQL ; « n'existe pas » et « pas à vous » renvoient la même 404 |

Surface d'attaque du conteneur : image `distroless` sans shell, utilisateur non
root, `cap_drop: ALL`, `no-new-privileges`, système de fichiers en lecture
seule, base sur un réseau Docker interne sans port publié.

**Ce qui n'est volontairement pas protégé :** l'URL d'une roue publiée est un
secret partagé. Qui possède le lien `/embed/{uuid}` peut la voir et la faire
tourner — c'est le comportement voulu pour une intégration Digipad sans compte.

---

## 8. Journalisation

Trois niveaux, tous exploitables :

1. **Journal d'accès** — une ligne JSON par requête sur la sortie standard :
   `request_id`, méthode, chemin, statut, durée, IP, user-agent.

   ```bash
   docker compose logs -f app
   ```

2. **Table `audit_log`** — une ligne par action ayant un effet : connexion,
   refus de connexion, création/modification/suppression, upload, tirage,
   blocage CSRF. Le `request_id` permet de recoller avec le journal d'accès.

3. **Table `spins`** — l'historique complet des tirages, avec l'IP hachée et
   salée (pseudonymisée, non réversible par table arc-en-ciel).

Quelques requêtes utiles :

```bash
docker compose exec db psql -U spinwheel -d spinwheel
```

```sql
-- Tentatives de connexion refusées, les plus récentes d'abord
SELECT created_at, action, details->>'email' AS email, details->>'raison' AS raison, ip
FROM audit_log
WHERE action IN ('auth.login_denied', 'auth.login_failed')
ORDER BY id DESC
LIMIT 50;

-- Répartition des tirages d'une roue
SELECT segment_label, count(*)
FROM spins
WHERE wheel_id = '...'
GROUP BY segment_label
ORDER BY count(*) DESC;

-- Toutes les actions d'un compte
SELECT a.created_at, a.action, a.entity_type, a.details
FROM audit_log a
JOIN users u ON u.id = a.actor_id
WHERE u.email = 'prenom.nom@exemple.fr'
ORDER BY a.id DESC
LIMIT 100;
```

L'horodatage étant encodé dans l'UUIDv7, `ORDER BY id` équivaut à un tri
chronologique, sans lire la colonne `created_at`.

---

## 9. Exploitation

**Sauvegarde**

La base exige un mot de passe même sur le socket local (`--auth-local=scram-sha-256`),
et `pg_dump` ne lit pas `POSTGRES_PASSWORD` : sans `PGPASSWORD`, la commande
échoue, `gzip` écrit une archive vide **et le code de retour reste 0**. D'où le
`set -o pipefail` et le contrôle de taille, sans lesquels une sauvegarde ratée
est indiscernable d'une sauvegarde réussie.

```bash
mkdir -p /var/backups/spinwheel && cd /var/backups/spinwheel
set -o pipefail
docker compose -f /chemin/vers/docker-compose.yml exec -T db \
  sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' \
  | gzip > base-$(date +%F).sql.gz
test "$(stat -c %s base-$(date +%F).sql.gz)" -gt 10000 || echo "SAUVEGARDE SUSPECTE"
docker run --rm -v spin-the-wheel_images:/data -v "$PWD":/sortie alpine \
  tar czf /sortie/images-$(date +%F).tar.gz -C /data .
```

Écrire hors de l'arbre du dépôt : la racine du projet est suivie par git et
poussée sur GitHub.

**Restauration**

```bash
gunzip -c base-2026-08-09.sql.gz | docker compose exec -T db \
  sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
docker run --rm -v spin-the-wheel_images:/data -v "$PWD":/entree alpine \
  tar xzf /entree/images-2026-08-09.tar.gz -C /data
```

**Mise à jour**

```bash
git pull && docker compose up -d --build
```

Le `--build` n'est pas optionnel, et son oubli est trompeur. `frontend/` est
monté en bind mount : un simple `git pull` met le HTML, le CSS et le JavaScript
à jour immédiatement. Le binaire Go, lui, vit dans l'image, et `docker compose
up -d` sans `--build` réutilise l'image existante. On se retrouve alors avec un
front récent sur un serveur ancien — le navigateur n'affiche plus les
restrictions levées, mais l'API les applique toujours.

Vérifier ce qui tourne réellement :

```bash
docker image inspect $(docker compose images -q app) --format '{{.Created}}'
```

Les migrations non encore jouées s'appliquent au démarrage.

**Ménage automatique** — une tâche horaire supprime les sessions et états OAuth
périmés, ainsi que les images qu'aucun segment ne référence depuis plus de 24 h.

---

## 10. Développement local

Sur le Mac, avec Docker :

```bash
cp .env.example .env
```

Adapter pour du HTTP en clair sur localhost :

```
APP_ENV=development
BASE_URL=http://localhost:8080
COOKIE_SECURE=false
TRUST_PROXY=false
EMBED_FRAME_ANCESTORS='self' http://localhost:8080
```

Ajouter `http://localhost:8080/auth/google/callback` aux URI de redirection
autorisés du client OAuth, puis :

```bash
docker compose up --build
```

Sans Docker, avec Go 1.25 et un PostgreSQL local :

```bash
cd backend && go build ./... && go vet ./...
```

---

## 11. Arborescence

```
backend/
  cmd/server/main.go            point d'entrée, arrêt propre, tâche de ménage
  internal/config/              lecture et validation de l'environnement
  internal/logging/             journal JSON structuré
  internal/uid/                 UUIDv7 (génération, analyse, horodatage)
  internal/store/               pool pgx, migrations, requêtes, audit
  internal/httpx/               intergiciels, en-têtes de sécurité, débit
  internal/auth/                OAuth Google, sessions, CSRF
  internal/imgproc/             validation et ré-encodage des images
  internal/api/                 routeur et gestionnaires HTTP
frontend/
  index.html edit.html accounts.html embed.html
  static/css/                   app.css, embed.css
  static/js/                    api, dom, session, wheel, app, edit, accounts, embed
docker-compose.yml
.env.example
```
