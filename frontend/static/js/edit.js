// Éditeur d'une roue : réglages, segments, aperçu, intégration, historique.

import * as api from './api.js';
import { el, replace, status, formatDate } from './dom.js';
import { mountHeader } from './session.js';
import { createWheel } from './wheel.js';

// Palette de départ, reprise en boucle quand on ajoute des segments.
const PALETTE = [
  '#2563eb',
  '#f59e0b',
  '#16a34a',
  '#dc2626',
  '#7c3aed',
  '#0891b2',
  '#db2777',
  '#65a30d',
];

const MAX_SEGMENTS = 64;
const MAX_OCTETS_IMAGE = 2 * 1024 * 1024;
const TYPES_IMAGE = 'image/png,image/jpeg,image/gif,image/webp';

// L'URL de la page est /wheels/{id}.
const wheelId = window.location.pathname.split('/').filter(Boolean)[1] ?? '';

const zoneStatut = document.getElementById('statut');
const champTitre = document.getElementById('titre');
const champActive = document.getElementById('active');
const listeSegments = document.getElementById('segments');
const boutonAjouter = document.getElementById('ajouter-segment');
const boutonEnregistrer = document.getElementById('enregistrer');
const boutonSupprimer = document.getElementById('supprimer');
const lienEmbed = document.getElementById('lien-embed');
const codeEmbed = document.getElementById('code-embed');
const boutonCopier = document.getElementById('copier-embed');
const listeTirages = document.getElementById('tirages');
const tiragesVides = document.getElementById('tirages-vides');
const boutonRafraichir = document.getElementById('rafraichir-tirages');

/** @type {{label: string, color: string, image_id: string, image_url: string}[]} */
let segments = [];
let apercu = null;

// --- Aperçu -----------------------------------------------------------------

function rafraichirApercu() {
  apercu.setSegments(
    segments.map((seg) => ({
      label: seg.label,
      color: seg.color,
      image_url: seg.image_url,
    })),
  );
}

// --- Segments ---------------------------------------------------------------

// Un nouveau quartier arrive sans texte : le libellé ne s'ajoute que si on
// écrit dedans. Seule la couleur est proposée.
function segmentVide(index) {
  return {
    label: '',
    color: PALETTE[index % PALETTE.length],
    image_id: '',
    image_url: '',
  };
}

function deplacer(index, delta) {
  const cible = index + delta;
  if (cible < 0 || cible >= segments.length) return;
  const [item] = segments.splice(index, 1);
  segments.splice(cible, 0, item);
  dessinerSegments();
}

function supprimer(index) {
  segments.splice(index, 1);
  dessinerSegments();
}

async function televerser(fichier, index) {
  if (!fichier) return;
  if (fichier.size > MAX_OCTETS_IMAGE) {
    status(zoneStatut, 'Image trop volumineuse (2 Mio maximum).', 'erreur');
    return;
  }

  status(zoneStatut, 'Envoi de l’image…', 'info');
  const donnees = new FormData();
  donnees.append('image', fichier);

  try {
    const reponse = await api.post('/api/images', donnees);
    segments[index].image_id = reponse.image.id;
    segments[index].image_url = reponse.image.url;
    dessinerSegments();
    status(zoneStatut, 'Image ajoutée. Pensez à enregistrer.', 'succes');
  } catch (err) {
    status(zoneStatut, err.message, 'erreur');
  }
}

function ligneSegment(seg, index) {
  const champFichier = el('input', {
    class: 'champ-fichier',
    attrs: { type: 'file', accept: TYPES_IMAGE },
    on: {
      change: (event) => {
        const fichier = event.target.files && event.target.files[0];
        event.target.value = '';
        televerser(fichier, index);
      },
    },
  });

  const blocImage = el('div', { class: 'segment__image' }, [
    champFichier,
    seg.image_url
      ? el('img', {
          class: 'segment__vignette',
          attrs: { src: seg.image_url, alt: '', width: 34, height: 34 },
        })
      : null,
    el('button', {
      class: 'icone-bouton',
      text: seg.image_url ? '⟳' : '🖼',
      attrs: {
        type: 'button',
        title: seg.image_url ? 'Remplacer l’image' : 'Ajouter une image',
      },
      on: { click: () => champFichier.click() },
    }),
    seg.image_url
      ? el('button', {
          class: 'icone-bouton',
          text: '⌫',
          attrs: { type: 'button', title: 'Retirer l’image' },
          on: {
            click: () => {
              segments[index].image_id = '';
              segments[index].image_url = '';
              dessinerSegments();
            },
          },
        })
      : null,
  ]);

  return el('li', { class: 'segment' }, [
    el('span', { class: 'segment__poignee', text: String(index + 1) }),

    el('div', { class: 'segment__libelle' }, [
      el('input', {
        attrs: {
          type: 'text',
          value: seg.label,
          maxlength: 80,
          'aria-label': `Libellé du segment ${index + 1}`,
          placeholder: 'Libellé (facultatif)',
        },
        on: {
          input: (event) => {
            segments[index].label = event.target.value;
            rafraichirApercu();
          },
        },
      }),
    ]),

    el('input', {
      attrs: {
        type: 'color',
        value: seg.color,
        'aria-label': `Couleur du segment ${index + 1}`,
      },
      on: {
        input: (event) => {
          segments[index].color = event.target.value;
          rafraichirApercu();
        },
      },
    }),

    blocImage,

    el('div', { class: 'segment__actions' }, [
      el('button', {
        class: 'icone-bouton',
        text: '↑',
        attrs: { type: 'button', title: 'Monter', disabled: index === 0 },
        on: { click: () => deplacer(index, -1) },
      }),
      el('button', {
        class: 'icone-bouton',
        text: '↓',
        attrs: {
          type: 'button',
          title: 'Descendre',
          disabled: index === segments.length - 1,
        },
        on: { click: () => deplacer(index, 1) },
      }),
      el('button', {
        class: 'icone-bouton',
        text: '✕',
        attrs: { type: 'button', title: 'Supprimer le segment' },
        on: { click: () => supprimer(index) },
      }),
    ]),
  ]);
}

function dessinerSegments() {
  replace(listeSegments, ...segments.map(ligneSegment));
  boutonAjouter.disabled = segments.length >= MAX_SEGMENTS;
  rafraichirApercu();
}

// --- Enregistrement ---------------------------------------------------------

async function enregistrer() {
  const titre = champTitre.value.trim();
  if (!titre) {
    status(zoneStatut, 'Le titre est obligatoire.', 'erreur');
    return;
  }
  if (segments.length < 2) {
    status(zoneStatut, 'Une roue demande au moins 2 segments.', 'erreur');
    return;
  }

  boutonEnregistrer.disabled = true;
  status(zoneStatut, 'Enregistrement…', 'info');

  try {
    await api.patch(`/api/wheels/${encodeURIComponent(wheelId)}`, {
      title: titre,
      is_active: champActive.checked,
    });

    const reponse = await api.put(`/api/wheels/${encodeURIComponent(wheelId)}/segments`, {
      segments: segments.map((seg) => ({
        label: seg.label,
        color: seg.color,
        image_id: seg.image_id || '',
      })),
    });

    segments = (reponse.segments ?? []).map((seg) => ({
      label: seg.label,
      color: seg.color,
      image_id: seg.image_id ?? '',
      image_url: seg.image_url ?? '',
    }));
    dessinerSegments();
    status(zoneStatut, 'Roue enregistrée.', 'succes');
  } catch (err) {
    status(zoneStatut, err.message, 'erreur');
  } finally {
    boutonEnregistrer.disabled = false;
  }
}

async function supprimerRoue() {
  const confirme = window.confirm(
    'Supprimer cette roue ? Le lien d’intégration cessera de fonctionner.',
  );
  if (!confirme) return;

  boutonSupprimer.disabled = true;
  try {
    await api.del(`/api/wheels/${encodeURIComponent(wheelId)}`);
    window.location.assign('/');
  } catch (err) {
    status(zoneStatut, err.message, 'erreur');
    boutonSupprimer.disabled = false;
  }
}

// --- Historique des tirages -------------------------------------------------

async function chargerTirages() {
  try {
    const data = await api.get(`/api/wheels/${encodeURIComponent(wheelId)}/spins`);
    const tirages = data.spins ?? [];
    replace(
      listeTirages,
      ...tirages.map((tirage) =>
        el('li', {}, [
          el('span', { text: tirage.segment_label }),
          el('span', { class: 'tirage-date', text: formatDate(tirage.created_at) }),
        ]),
      ),
    );
    tiragesVides.hidden = tirages.length > 0;
  } catch (err) {
    status(zoneStatut, err.message, 'erreur');
  }
}

// --- Intégration ------------------------------------------------------------

function preparerIntegration(url) {
  lienEmbed.value = url;
  // Affecté via `value` : c'est du texte dans un champ, jamais du balisage
  // interprété par la page.
  codeEmbed.value =
    `<iframe src="${url}" width="480" height="640" ` +
    `style="border:0;max-width:100%" title="Spin the Wheel" ` +
    `loading="lazy"></iframe>`;
}

async function copierIntegration() {
  try {
    await navigator.clipboard.writeText(codeEmbed.value);
    status(zoneStatut, 'Code iframe copié.', 'succes');
  } catch {
    codeEmbed.select();
    status(zoneStatut, 'Copie automatique refusée : le code est sélectionné.', 'info');
  }
}

// --- Démarrage --------------------------------------------------------------

async function main() {
  const account = await mountHeader();
  if (!account) return;

  apercu = createWheel(document.getElementById('roue'));

  boutonAjouter.addEventListener('click', () => {
    if (segments.length >= MAX_SEGMENTS) return;
    segments.push(segmentVide(segments.length));
    dessinerSegments();
  });
  boutonEnregistrer.addEventListener('click', enregistrer);
  boutonSupprimer.addEventListener('click', supprimerRoue);
  boutonCopier.addEventListener('click', copierIntegration);
  boutonRafraichir.addEventListener('click', chargerTirages);
  champTitre.addEventListener('input', () => {
    document.title = `${champTitre.value || 'Roue'} — Spin the Wheel`;
  });

  try {
    const data = await api.get(`/api/wheels/${encodeURIComponent(wheelId)}`);
    champTitre.value = data.wheel.title;
    champActive.checked = data.wheel.is_active;
    document.title = `${data.wheel.title} — Spin the Wheel`;
    preparerIntegration(data.embed_url);

    segments = (data.segments ?? []).map((seg) => ({
      label: seg.label,
      color: seg.color,
      image_id: seg.image_id ?? '',
      image_url: seg.image_url ?? '',
    }));

    // Une roue neuve part avec deux segments pour donner un point de départ.
    if (segments.length === 0) {
      segments = [segmentVide(0), segmentVide(1)];
    }

    dessinerSegments();
    await chargerTirages();
  } catch (err) {
    if (err.status === 404) {
      status(zoneStatut, 'Cette roue est introuvable.', 'erreur');
      return;
    }
    status(zoneStatut, err.message, 'erreur');
  }
}

main();
