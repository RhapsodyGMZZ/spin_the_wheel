// Rendu de la roue sur un canvas 2D.
//
// Le choix du canvas n'est pas qu'esthétique : les libellés sont peints avec
// `fillText`, jamais insérés dans le DOM. Un libellé contenant du balisage est
// donc dessiné littéralement, caractère par caractère. Il n'existe aucun
// chemin par lequel le contenu d'une roue devienne du HTML.

const TAU = Math.PI * 2;

/** Ramène un angle dans [0, 2π). */
function normalize(angle) {
  const a = angle % TAU;
  return a < 0 ? a + TAU : a;
}

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

/**
 * Indique si le système demande de limiter les animations.
 *
 * Sur macOS c'est « Réduire les animations » dans les réglages d'accessibilité,
 * activé par bien des gens que les animations de fenêtres incommodent — sans
 * qu'ils souhaitent pour autant qu'une roue de la fortune cesse de tourner.
 */
function prefereMoinsDeMouvement() {
  return (
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}

/**
 * Choisit une encre lisible sur un fond donné.
 * Luminance relative WCAG : au-delà du seuil, on écrit en sombre.
 */
export function readableInk(hex) {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex || '');
  if (!m) return '#111827';
  const toLinear = (v) => {
    const c = parseInt(v, 16) / 255;
    return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  };
  const l = 0.2126 * toLinear(m[1]) + 0.7152 * toLinear(m[2]) + 0.0722 * toLinear(m[3]);
  return l > 0.42 ? '#111827' : '#ffffff';
}

/**
 * Crée un contrôleur de roue attaché à un canvas.
 * Le canvas doit être carré (aspect-ratio: 1 en CSS).
 */
export function createWheel(canvas) {
  const ctx = canvas.getContext('2d');
  const images = new Map();

  let segments = [];
  let rotation = 0;
  let frame = null;
  let spinning = false;

  function preload() {
    for (const seg of segments) {
      const url = seg.image_url;
      if (!url || images.has(url)) continue;
      images.set(url, null);
      const img = new Image();
      img.decoding = 'async';
      img.addEventListener('load', () => {
        images.set(url, img);
        render();
      });
      img.addEventListener('error', () => images.set(url, null));
      img.src = url;
    }
  }

  /**
   * Ajuste la résolution du canvas à sa taille CSS et à la densité d'écran.
   *
   * La mesure passe par getBoundingClientRect plutôt que clientWidth : elle
   * est fractionnaire, donc juste même quand la mise en page tombe sur des
   * demi-pixels. Et le facteur d'échelle est recalculé à partir du nombre de
   * pixels réellement alloués, pas de devicePixelRatio brut : sans cela,
   * l'arrondi décale le dessin d'une fraction de pixel à chaque redimension.
   */
  function fit() {
    const dpr = window.devicePixelRatio || 1;
    const rect = canvas.getBoundingClientRect();
    const cssSize = Math.max(1, rect.width || canvas.clientWidth || canvas.width);
    const pixels = Math.max(1, Math.round(cssSize * dpr));

    if (canvas.width !== pixels || canvas.height !== pixels) {
      canvas.width = pixels;
      canvas.height = pixels;
    }
    const echelle = pixels / cssSize;
    ctx.setTransform(echelle, 0, 0, echelle, 0, 0);
    return cssSize;
  }

  function ellipsize(text, maxWidth) {
    if (ctx.measureText(text).width <= maxWidth) return text;
    let cut = text;
    while (cut.length > 1 && ctx.measureText(`${cut}…`).width > maxWidth) {
      cut = cut.slice(0, -1);
    }
    return `${cut}…`;
  }

  function drawSegmentContent(seg, mid, arc, radius) {
    ctx.save();
    ctx.rotate(mid);

    const img = seg.image_url ? images.get(seg.image_url) : null;
    if (img) {
      // Taille bornée par l'ouverture du segment : sur une roue à 20
      // quartiers, l'image doit rétrécir pour ne pas déborder chez le voisin.
      const byArc = arc * radius * 0.40;
      const side = clamp(Math.min(radius * 0.22, byArc), 12, 56);
      const ratio =
        img.naturalWidth && img.naturalHeight ? img.naturalWidth / img.naturalHeight : 1;
      const w = ratio >= 1 ? side : side * ratio;
      const h = ratio >= 1 ? side / ratio : side;
      ctx.drawImage(img, radius * 0.42 - w / 2, -h / 2, w, h);
    }

    const fontSize = clamp(radius * 0.072, 9, 19);
    ctx.font = `600 ${fontSize}px system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`;
    ctx.fillStyle = readableInk(seg.color);
    ctx.textBaseline = 'middle';

    const label = ellipsize(String(seg.label ?? ''), radius * 0.46);
    // Sur la moitié gauche de la roue, le texte s'écrirait à l'envers : on le
    // retourne et on l'aligne de l'autre côté.
    const upsideDown = (() => {
      const abs = normalize(rotation + mid);
      return abs > Math.PI / 2 && abs < (3 * Math.PI) / 2;
    })();

    if (upsideDown) {
      ctx.rotate(Math.PI);
      ctx.textAlign = 'left';
      ctx.fillText(label, -radius * 0.94, 0);
    } else {
      ctx.textAlign = 'right';
      ctx.fillText(label, radius * 0.94, 0);
    }

    ctx.restore();
  }

  function drawEmpty(size, radius) {
    ctx.beginPath();
    ctx.arc(size / 2, size / 2, radius, 0, TAU);
    ctx.fillStyle = '#f1f5f9';
    ctx.fill();
    ctx.strokeStyle = '#cbd5e1';
    ctx.lineWidth = 2;
    ctx.stroke();

    ctx.fillStyle = '#64748b';
    ctx.font = '500 15px system-ui, -apple-system, "Segoe UI", Roboto, sans-serif';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText('Aucun segment', size / 2, size / 2);
  }

  function drawHub(size) {
    ctx.beginPath();
    ctx.arc(size / 2, size / 2, Math.max(12, size * 0.055), 0, TAU);
    ctx.fillStyle = '#ffffff';
    ctx.fill();
    ctx.strokeStyle = '#0f172a';
    ctx.lineWidth = 3;
    ctx.stroke();
  }

  function drawPointer(size, radius) {
    ctx.save();
    ctx.translate(size / 2, size / 2 - radius);
    ctx.beginPath();
    ctx.moveTo(0, Math.max(14, radius * 0.10));
    ctx.lineTo(-Math.max(9, radius * 0.055), -Math.max(5, radius * 0.03));
    ctx.lineTo(Math.max(9, radius * 0.055), -Math.max(5, radius * 0.03));
    ctx.closePath();
    ctx.fillStyle = '#0f172a';
    ctx.fill();
    ctx.strokeStyle = '#ffffff';
    ctx.lineWidth = 2.5;
    ctx.lineJoin = 'round';
    ctx.stroke();
    ctx.restore();
  }

  function render() {
    const size = fit();
    const radius = size / 2 - Math.max(6, size * 0.03);
    ctx.clearRect(0, 0, size, size);

    if (segments.length === 0) {
      drawEmpty(size, radius);
      drawPointer(size, radius);
      return;
    }

    const arc = TAU / segments.length;

    ctx.save();
    ctx.translate(size / 2, size / 2);
    ctx.rotate(rotation);

    for (let i = 0; i < segments.length; i += 1) {
      ctx.beginPath();
      ctx.moveTo(0, 0);
      ctx.arc(0, 0, radius, i * arc, (i + 1) * arc);
      ctx.closePath();
      ctx.fillStyle = segments[i].color || '#cbd5e1';
      ctx.fill();
      ctx.strokeStyle = 'rgba(255,255,255,0.9)';
      ctx.lineWidth = segments.length > 24 ? 1 : 2;
      ctx.stroke();
    }

    for (let i = 0; i < segments.length; i += 1) {
      drawSegmentContent(segments[i], i * arc + arc / 2, arc, radius);
    }

    ctx.restore();

    ctx.beginPath();
    ctx.arc(size / 2, size / 2, radius, 0, TAU);
    ctx.strokeStyle = '#0f172a';
    ctx.lineWidth = 3;
    ctx.stroke();

    drawHub(size);
    drawPointer(size, radius);
  }

  function cancelAnimation() {
    if (frame !== null) {
      cancelAnimationFrame(frame);
      frame = null;
    }
  }

  /**
   * Fait tourner la roue jusqu'à ce que le segment `index` s'arrête sous le
   * repère, en haut. L'index vient du serveur : l'animation ne décide de rien.
   * @returns {Promise<void>}
   */
  function spinTo(index, options = {}) {
    // La rotation EST le contenu de la page, pas un ornement : la supprimer
    // reviendrait à retirer la fonction. Quand le système demande de réduire
    // les animations, on raccourcit — deux tours au lieu de six — mais la roue
    // tourne toujours.
    const menage = prefereMoinsDeMouvement();
    const { turns = menage ? 2 : 6, duration = menage ? 2400 : 4600 } = options;

    return new Promise((resolve) => {
      const count = segments.length;
      if (count === 0) {
        resolve();
        return;
      }
      const arc = TAU / count;
      // Le repère est en haut, soit -π/2 dans le repère du canvas.
      const target = -Math.PI / 2 - (index + 0.5) * arc;
      let delta = (target - rotation) % TAU;
      if (delta < 0) delta += TAU;

      // Le nombre de tours doit être ENTIER. L'alignement sur le segment est
      // porté par `delta` ; seule une rotation d'un multiple exact de 2π le
      // préserve. Un tour et demi ferait s'arrêter la roue à l'opposé du nom
      // annoncé — l'arrondi rend cette erreur impossible depuis l'appelant.
      const tours = Math.max(1, Math.round(turns));

      const from = rotation;
      const to = from + tours * TAU + delta;

      cancelAnimation();

      // Seul un appelant demandant explicitement une durée nulle obtient un
      // saut immédiat.
      if (duration <= 0) {
        rotation = normalize(to);
        render();
        resolve();
        return;
      }

      spinning = true;
      const started = performance.now();
      const step = (now) => {
        const t = Math.min(1, (now - started) / duration);
        // Décélération marquée : vive au départ, très douce à l'arrivée.
        const eased = 1 - Math.pow(1 - t, 4);
        rotation = from + (to - from) * eased;
        render();
        if (t < 1) {
          frame = requestAnimationFrame(step);
        } else {
          rotation = normalize(to);
          frame = null;
          spinning = false;
          render();
          resolve();
        }
      };
      frame = requestAnimationFrame(step);
    });
  }

  // Redimensionner le canvas change sa taille intrinsèque, ce qui peut à son
  // tour modifier la mise en page. ResizeObserver n'en notifie pas la seconde
  // vague dans la même image : sans un deuxième passage, la résolution reste
  // en retard d'un cran et la roue s'affiche étirée.
  let secondPassage = null;
  function renderStable() {
    render();
    if (secondPassage !== null) cancelAnimationFrame(secondPassage);
    secondPassage = requestAnimationFrame(() => {
      secondPassage = null;
      render();
    });
  }

  const observer =
    typeof ResizeObserver === 'function' ? new ResizeObserver(renderStable) : null;
  if (observer) observer.observe(canvas);

  // Un MacBook branché sur un écran externe change de densité en cours de
  // route : la seule notification fiable est une media query sur la
  // résolution courante, réarmée à chaque changement.
  let densite = null;
  function surChangementDensite() {
    renderStable();
    surveillerDensite();
  }
  function surveillerDensite() {
    if (typeof window.matchMedia !== 'function') return;
    if (densite) densite.removeEventListener('change', surChangementDensite);
    densite = window.matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
    densite.addEventListener('change', surChangementDensite, { once: true });
  }
  surveillerDensite();

  return {
    /** Remplace les segments affichés. */
    setSegments(next) {
      segments = Array.isArray(next) ? next.slice() : [];
      preload();
      render();
    },
    render,
    spinTo,
    isSpinning: () => spinning,
    destroy() {
      cancelAnimation();
      if (secondPassage !== null) cancelAnimationFrame(secondPassage);
      if (observer) observer.disconnect();
      if (densite) densite.removeEventListener('change', surChangementDensite);
    },
  };
}
