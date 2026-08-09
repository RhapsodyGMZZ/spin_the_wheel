// Client HTTP du front.
//
// Deux règles tiennent tout le reste :
//   - `credentials: 'same-origin'` — le cookie de session ne part jamais vers
//     une autre origine ;
//   - toute requête modifiante porte l'en-tête X-CSRF-Token, dont la valeur
//     vient de /api/me et n'est jamais stockée dans un cookie lisible par le
//     script.

export class ApiError extends Error {
  constructor(message, status, code) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

const SAFE_METHODS = new Set(['GET', 'HEAD']);

let csrfToken = null;
let currentUser = null;

/** Compte connecté, ou null. */
export function currentAccount() {
  return currentUser;
}

/** Interroge /api/me : établit l'état de connexion et récupère le jeton CSRF. */
export async function loadSession() {
  const res = await fetch('/api/me', {
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  });
  if (!res.ok) {
    throw new ApiError('Session illisible.', res.status, 'session');
  }
  const data = await res.json();
  currentUser = data.authenticated ? data.user : null;
  csrfToken = data.csrf_token ?? null;
  return currentUser;
}

async function parseResponse(res) {
  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = null;
    }
  }
  if (!res.ok) {
    const payload = (data && data.error) || {};
    throw new ApiError(
      payload.message || `Erreur ${res.status}.`,
      res.status,
      payload.code || 'inconnu',
    );
  }
  return data;
}

async function request(method, path, body) {
  const headers = { Accept: 'application/json' };
  const init = { method, credentials: 'same-origin', headers };

  if (!SAFE_METHODS.has(method)) {
    if (!csrfToken) await loadSession();
    headers['X-CSRF-Token'] = csrfToken ?? '';
  }

  if (body !== undefined && body !== null) {
    if (body instanceof FormData) {
      // Pas de Content-Type manuel : le navigateur ajoute la frontière
      // multipart, qu'on ne peut pas deviner ici.
      init.body = body;
    } else {
      headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(body);
    }
  }

  return parseResponse(await fetch(path, init));
}

export const get = (path) => request('GET', path);
export const post = (path, body) => request('POST', path, body);
export const patch = (path, body) => request('PATCH', path, body);
export const put = (path, body) => request('PUT', path, body);
export const del = (path) => request('DELETE', path);

/**
 * Exige une session ouverte. Renvoie le compte, ou redirige vers l'accueil.
 */
export async function requireAccount() {
  const account = await loadSession();
  if (!account) {
    window.location.replace('/');
    return null;
  }
  return account;
}
