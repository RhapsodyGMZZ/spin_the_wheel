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

async function tourner() {
  if (roue.isSpinning()) return;

  bouton.disabled = true;
  resultat.textContent = '';
  afficherMessage('');

  try {
    // Le serveur choisit le résultat ; l'animation ne fait que s'y rendre.
    const tirage = await appeler('POST', `/api/embed/${encodeURIComponent(wheelId)}/spin`);
    await roue.spinTo(tirage.index);
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
    const data = await appeler('GET', `/api/embed/${encodeURIComponent(wheelId)}`);

    titre.textContent = data.title;
    document.title = data.title || 'Roue de la fortune';
    roue.setSegments(data.segments ?? []);

    if ((data.segments ?? []).length < 2) {
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
