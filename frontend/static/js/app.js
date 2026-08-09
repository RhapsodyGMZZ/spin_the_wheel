// Tableau de bord : connexion, liste des roues, création.

import * as api from './api.js';
import { el, replace, status } from './dom.js';
import { mountHeader } from './session.js';

// Messages de connexion. Le serveur ne renvoie qu'un code appartenant à cet
// ensemble fermé ; aucun texte venant de l'extérieur n'est affiché.
const MESSAGES_ERREUR = {
  non_autorise:
    "Ce compte Google ne figure pas parmi les comptes autorisés. Demandez son ajout à une personne déjà autorisée.",
  email_non_verifie: "L'adresse de ce compte Google n'est pas vérifiée.",
  etat_invalide: 'La tentative de connexion a expiré. Réessayez.',
  refus_google: 'Connexion annulée.',
  google: "Google n'a pas pu confirmer la connexion. Réessayez dans un instant.",
  requete_invalide: 'Requête de connexion incomplète.',
  interne: 'Erreur interne. Réessayez dans un instant.',
};

const zoneStatut = document.getElementById('statut');
const ecranConnexion = document.getElementById('ecran-connexion');
const ecranRoues = document.getElementById('ecran-roues');
const listeRoues = document.getElementById('liste-roues');
const listeVide = document.getElementById('liste-vide');
const formCreation = document.getElementById('form-creation');
const champTitre = document.getElementById('nouveau-titre');

/** Affiche l'éventuelle erreur de connexion passée en paramètre d'URL. */
function afficherErreurConnexion() {
  const params = new URLSearchParams(window.location.search);
  const code = params.get('erreur');
  if (!code) return;

  status(zoneStatut, MESSAGES_ERREUR[code] ?? 'La connexion a échoué.', 'erreur');

  // Nettoie l'URL pour qu'un rechargement ne réaffiche pas le message.
  window.history.replaceState(null, '', window.location.pathname);
}

function carteRoue(roue) {
  const segments =
    roue.segment_count === 1 ? '1 segment' : `${roue.segment_count} segments`;

  return el('li', { class: 'roue-carte' }, [
    el('a', {
      class: 'roue-carte__titre',
      text: roue.title,
      attrs: { href: `/wheels/${roue.id}` },
    }),
    el('div', { class: 'roue-carte__meta', text: segments }),
    el('div', {}, [
      roue.is_active
        ? el('span', { class: 'etiquette etiquette--active', text: 'Publiée' })
        : el('span', { class: 'etiquette', text: 'Brouillon' }),
    ]),
  ]);
}

async function chargerRoues() {
  const data = await api.get('/api/wheels');
  const roues = data.wheels ?? [];

  replace(listeRoues, ...roues.map(carteRoue));
  listeVide.hidden = roues.length > 0;
}

async function creerRoue(event) {
  event.preventDefault();

  const titre = champTitre.value.trim();
  if (!titre) {
    status(zoneStatut, 'Donnez un titre à la roue.', 'erreur');
    return;
  }

  const bouton = formCreation.querySelector('button[type="submit"]');
  bouton.disabled = true;
  try {
    const data = await api.post('/api/wheels', { title: titre });
    window.location.assign(`/wheels/${data.wheel.id}`);
  } catch (err) {
    status(zoneStatut, err.message, 'erreur');
    bouton.disabled = false;
  }
}

async function main() {
  afficherErreurConnexion();

  // Cette page est la seule accessible sans session : elle porte l'écran de
  // connexion.
  const account = await mountHeader({ requireLogin: false });
  if (!account) {
    ecranConnexion.hidden = false;
    return;
  }

  ecranRoues.hidden = false;
  formCreation.addEventListener('submit', creerRoue);

  try {
    await chargerRoues();
  } catch (err) {
    status(zoneStatut, err.message, 'erreur');
  }
}

main();
