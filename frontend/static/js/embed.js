// Page intégrée en iframe : afficher la roue, la faire tourner, annoncer.
//
// Volontairement autonome : aucune notion de session, aucun jeton CSRF, aucun
// appel d'écriture en dehors du tirage. Même compromise, cette page ne peut
// pas modifier une roue — les routes d'édition exigent une session, et le
// cookie de session n'accompagne jamais une requête venue d'un autre site.

import { createWheel } from './wheel.js';
import { createConfetti } from './confetti.js';

// L'URL de la page est /embed/{id}.
const wheelId = window.location.pathname.split('/').filter(Boolean)[1] ?? '';

const titre = document.getElementById('titre');
const bouton = document.getElementById('tourner');
const message = document.getElementById('message');
const resultat = document.getElementById('resultat');
const annonce = document.getElementById('annonce');
const annonceImage = document.getElementById('annonce-image');
const annonceTexte = document.getElementById('annonce-texte');

let roue = null;
let confettis = null;
let segments = [];

function afficherMessage(texte) {
  message.textContent = texte;
  message.hidden = texte === '';
}

function masquerAnnonce() {
  annonce.hidden = true;
  annonceTexte.textContent = '';
  annonceImage.hidden = true;
  annonceImage.removeAttribute('src');
  resultat.textContent = '';
  confettis.arreter();
}

function afficherAnnonce(tirage) {
  const libelle = String(tirage.label ?? '');

  if (tirage.image_url) {
    annonceImage.src = tirage.image_url;
    annonceImage.hidden = false;
  } else {
    annonceImage.hidden = true;
    annonceImage.removeAttribute('src');
  }

  // Un segment sans libellé ni image reste identifiable par son rang.
  annonceTexte.textContent =
    libelle || (tirage.image_url ? '' : `Segment ${tirage.index + 1}`);

  annonce.hidden = false;
  // Doublure pour les lecteurs d'écran, hors du visuel.
  resultat.textContent = libelle || `Segment ${tirage.index + 1}`;

  // Les confettis font partie de l'annonce du résultat, comme la rotation fait
  // partie du tirage : ils jouent toujours, réglage système ou pas. C'est un
  // choix produit assumé (demandé explicitement).
  confettis.lancer();
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
  document.title = data.title || 'Spin the Wheel';
  roue.setSegments(segments);
  return data;
}

async function tourner() {
  if (roue.isSpinning()) return;

  bouton.disabled = true;
  masquerAnnonce();
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
    const memeContenu =
      local &&
      String(local.label ?? '') === String(tirage.label ?? '') &&
      String(local.image_url ?? '') === String(tirage.image_url ?? '');
    if (!memeContenu) {
      await charger();
    }

    await roue.spinTo(tirage.index);
    // Le serveur fait foi, y compris si la resynchronisation a échoué.
    afficherAnnonce(tirage);
  } catch (err) {
    afficherMessage(err.message);
  } finally {
    bouton.disabled = false;
  }
}

async function main() {
  roue = createWheel(document.getElementById('roue'));
  confettis = createConfetti(document.getElementById('confettis'));

  try {
    await charger();

    if (segments.length < 2) {
      afficherMessage('Cette roue n’a pas encore assez de segments.');
      return;
    }

    bouton.disabled = false;
    bouton.addEventListener('click', tourner);
    // Cliquer l'annonce la referme, sans relancer de tirage.
    annonce.addEventListener('click', masquerAnnonce);
  } catch (err) {
    if (err.status === 404) {
      afficherMessage('Cette roue est introuvable ou n’est plus publiée.');
      return;
    }
    afficherMessage('La roue n’a pas pu être chargée.');
  }
}

main();
