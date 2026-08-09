// Gestion de la liste blanche des comptes autorisés.

import * as api from './api.js';
import { el, replace, status, formatDate } from './dom.js';
import { mountHeader } from './session.js';

const zoneStatut = document.getElementById('statut');
const formulaire = document.getElementById('form-ajout');
const champEmail = document.getElementById('email');
const champNote = document.getElementById('note');
const liste = document.getElementById('liste-comptes');

let compteCourant = null;

async function retirer(compte) {
  const avertissement =
    compte.email === compteCourant?.email
      ? 'Vous êtes sur le point de retirer VOTRE propre accès. Vous serez déconnecté immédiatement. Continuer ?'
      : `Retirer ${compte.email} ? Ses sessions ouvertes seront fermées.`;

  if (!window.confirm(avertissement)) return;

  try {
    await api.del(`/api/allowed-emails/${encodeURIComponent(compte.id)}`);
    if (compte.email === compteCourant?.email) {
      window.location.assign('/');
      return;
    }
    status(zoneStatut, `${compte.email} n’a plus accès.`, 'succes');
    await charger();
  } catch (err) {
    status(zoneStatut, err.message, 'erreur');
  }
}

function ligneCompte(compte) {
  return el('li', {}, [
    el('div', { class: 'compte-details' }, [
      el('span', { text: compte.email }),
      compte.note ? el('span', { class: 'compte-note', text: compte.note }) : null,
      el('span', {
        class: 'compte-note',
        text: `Autorisé le ${formatDate(compte.created_at)}`,
      }),
    ]),
    el('button', {
      class: 'bouton bouton--danger bouton--discret',
      text: 'Retirer',
      attrs: { type: 'button' },
      on: { click: () => retirer(compte) },
    }),
  ]);
}

async function charger() {
  const data = await api.get('/api/allowed-emails');
  replace(liste, ...(data.allowed_emails ?? []).map(ligneCompte));
}

async function ajouter(event) {
  event.preventDefault();

  const bouton = formulaire.querySelector('button[type="submit"]');
  bouton.disabled = true;

  try {
    await api.post('/api/allowed-emails', {
      email: champEmail.value.trim(),
      note: champNote.value.trim(),
    });
    champEmail.value = '';
    champNote.value = '';
    status(zoneStatut, 'Compte autorisé.', 'succes');
    await charger();
  } catch (err) {
    status(zoneStatut, err.message, 'erreur');
  } finally {
    bouton.disabled = false;
  }
}

async function main() {
  compteCourant = await mountHeader();
  if (!compteCourant) return;

  formulaire.addEventListener('submit', ajouter);

  try {
    await charger();
  } catch (err) {
    status(zoneStatut, err.message, 'erreur');
  }
}

main();
