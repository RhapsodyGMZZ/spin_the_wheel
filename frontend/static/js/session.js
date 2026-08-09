// En-tête commun aux pages authentifiées : affichage du compte et déconnexion.

import * as api from './api.js';

/**
 * Monte l'en-tête et renvoie le compte connecté.
 * @param {{requireLogin?: boolean}} options
 * @returns {Promise<object|null>}
 */
export async function mountHeader({ requireLogin = true } = {}) {
  let account = null;
  try {
    account = await api.loadSession();
  } catch {
    account = null;
  }

  if (!account) {
    if (requireLogin) window.location.replace('/');
    return null;
  }

  const nav = document.getElementById('nav');
  const compte = document.getElementById('compte');
  const deconnexion = document.getElementById('deconnexion');

  if (compte) compte.textContent = account.email;
  if (nav) nav.hidden = false;

  if (deconnexion) {
    deconnexion.addEventListener('click', async () => {
      deconnexion.disabled = true;
      try {
        await api.post('/api/logout');
      } catch {
        // La session est peut-être déjà close côté serveur : on repart
        // vers l'accueil dans tous les cas.
      }
      window.location.assign('/');
    });
  }

  return account;
}
