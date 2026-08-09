// Page intégrée en iframe : afficher la roue et la faire tourner.
//
// Volontairement autonome : aucune notion de session, aucun jeton CSRF, aucun
// appel d'écriture en dehors du tirage. Même compromise, cette page ne peut
// pas modifier une roue — les routes d'édition exigent une session, et le
// cookie de session n'accompagne jamais une requête venue d'un autre site.

import { createWheel } from './wheel.js';

// L'URL de la page est /embed/{id}.
const wheelId = window.location.pathname.split('/').filter(Boolean)[1] ?? '';

const titre = document.getElementById('titre');
const bouton = document.getElementById('tourner');
const resultat = document.getElementById('resultat');
const message = document.getElementById('message');

let roue = null;
let segments = [];

function afficherMessage(texte) {
  message.textContent = texte;
  message.hidden = texte === '';
}

async function appeler(method, path) {
  const reponse = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  });

  const texte = await reponse.text();
  let data = null;
  if (texte) {
    try {
      data = JSON.parse(texte);
    } catch {
      data = null;
    }
  }

  if (!reponse.ok) {
    const erreur = (data && data.error) || {};
    const e = new Error(erreur.message || `Erreur ${reponse.status}.`);
    e.status = reponse.status;
    throw e;
  }
  return data;
}

/** Recharge la roue depuis le serveur et met à jour l'affichage. */
async function charger() {
  const data = await appeler('GET', `/api/embed/${encodeURIComponent(wheelId)}`);
  segments = data.segments ?? [];
  titre.textContent = data.title;
  document.title = data.title || 'Roue de la fortune';
  roue.setSegments(segments);
  return data;
}

async function tourner() {
  if (roue.isSpinning()) return;

  bouton.disabled = true;
  resultat.textContent = '';
  afficherMessage('');

  try {
    // Le serveur choisit le résultat ; l'animation ne fait que s'y rendre.
    const tirage = await appeler('POST', `/api/embed/${encodeURIComponent(wheelId)}/spin`);

    // Ce cadre peut rester ouvert des heures dans un Digipad pendant que la
    // roue est rééditée ailleurs. Le serveur tire alors sur la liste courante
    // tandis que l'animation tourne sur une liste figée au chargement : le
    // repère s'arrêterait sur un nom et le texte en annoncerait un autre.
    // On resynchronise avant d'animer dès que les deux ne concordent plus.
    const local = segments[tirage.index];
    if (!local || local.label !== tirage.label) {
      await charger();
    }

    await roue.spinTo(tirage.index);
    // Le serveur fait foi, y compris si la resynchronisation a échoué.
    resultat.textContent = tirage.label;
  } catch (err) {
    afficherMessage(err.message);
  } finally {
    bouton.disabled = false;
  }
}

async function main() {
  roue = createWheel(document.getElementById('roue'));

  try {
    await charger();

    if (segments.length < 2) {
      afficherMessage('Cette roue n’a pas encore assez de segments.');
      return;
    }

    bouton.disabled = false;
    bouton.addEventListener('click', tourner);
  } catch (err) {
    if (err.status === 404) {
      afficherMessage('Cette roue est introuvable ou n’est plus publiée.');
      return;
    }
    afficherMessage('La roue n’a pas pu être chargée.');
  }
}

main();
