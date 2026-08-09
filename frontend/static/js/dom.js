// Fabrique d'éléments DOM.
//
// Aucune fonction de ce fichier n'écrit de HTML. Le texte passe toujours par
// `textContent`, les attributs par `setAttribute` : une donnée venant du
// serveur ou d'un champ de saisie ne peut pas devenir du balisage, quel que
// soit son contenu. C'est la contrepartie applicative de la CSP.

/**
 * Crée un élément.
 * @param {string} tag
 * @param {object} [props] class, text, attrs, on (écouteurs), dataset
 * @param {Array<Node|string>} [children]
 */
export function el(tag, props = {}, children = []) {
  const node = document.createElement(tag);

  if (props.class) node.className = props.class;
  if (props.text !== undefined) node.textContent = String(props.text);

  if (props.attrs) {
    for (const [key, value] of Object.entries(props.attrs)) {
      if (value === false || value === null || value === undefined) continue;
      node.setAttribute(key, value === true ? '' : String(value));
    }
  }
  if (props.on) {
    for (const [event, handler] of Object.entries(props.on)) {
      node.addEventListener(event, handler);
    }
  }
  if (props.dataset) {
    for (const [key, value] of Object.entries(props.dataset)) {
      node.dataset[key] = String(value);
    }
  }

  for (const child of children) {
    if (child === null || child === undefined) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

/** Vide un conteneur. */
export function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
}

/** Remplace le contenu d'un conteneur. */
export function replace(node, ...children) {
  clear(node);
  for (const child of children) {
    if (child === null || child === undefined) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
}

/**
 * Affiche un message dans une zone de statut.
 * @param {HTMLElement} node
 * @param {string} message
 * @param {'info'|'succes'|'erreur'} kind
 */
export function status(node, message, kind = 'info') {
  node.textContent = message;
  node.className = `statut statut--${kind}`;
  node.hidden = message === '';
}

/** Formate une date ISO en date lisible en français. */
export function formatDate(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString('fr-FR', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}
