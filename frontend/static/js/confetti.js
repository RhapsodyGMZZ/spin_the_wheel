// Confettis, en canvas 2D, sans dépendance.
//
// Deux canons partent des coins bas et tirent vers l'intérieur : la zone
// centrale, où s'affiche le résultat, reste dégagée au moment où l'œil s'y
// pose. Chaque morceau est un rectangle qui tombe, tourne et se retourne sur
// lui-même — l'aplatissement périodique suffit à donner l'illusion du papier
// qui virevolte, sans coût de rendu.

const COULEURS = [
  '#2563eb',
  '#f59e0b',
  '#16a34a',
  '#dc2626',
  '#7c3aed',
  '#0891b2',
  '#db2777',
  '#65a30d',
];

const GRAVITE = 1150; // px/s²
const FROTTEMENT = 0.62; // proportion de vitesse conservée par seconde

/**
 * Crée un lanceur de confettis attaché à un canvas en surimpression.
 * Le canvas doit être en `pointer-events: none` et couvrir la zone visible.
 */
export function createConfetti(canvas) {
  const ctx = canvas.getContext('2d');

  let morceaux = [];
  let frame = null;
  let precedent = 0;
  let echeance = 0;

  function ajuster() {
    const dpr = window.devicePixelRatio || 1;
    const l = Math.max(1, canvas.clientWidth || window.innerWidth);
    const h = Math.max(1, canvas.clientHeight || window.innerHeight);
    const pl = Math.round(l * dpr);
    const ph = Math.round(h * dpr);
    if (canvas.width !== pl || canvas.height !== ph) {
      canvas.width = pl;
      canvas.height = ph;
    }
    ctx.setTransform(pl / l, 0, 0, ph / h, 0, 0);
    return { l, h };
  }

  function canon(x, y, angle, nombre, l, h) {
    const echelle = Math.max(0.7, Math.min(1.6, l / 480));
    for (let i = 0; i < nombre; i += 1) {
      const ouverture = (Math.random() - 0.5) * 0.9;
      const vitesse = (620 + Math.random() * 520) * echelle;
      const a = angle + ouverture;
      morceaux.push({
        x,
        y,
        vx: Math.cos(a) * vitesse,
        vy: Math.sin(a) * vitesse,
        w: (6 + Math.random() * 6) * echelle,
        h: (9 + Math.random() * 8) * echelle,
        angle: Math.random() * Math.PI * 2,
        vAngle: (Math.random() - 0.5) * 12,
        battement: Math.random() * Math.PI * 2,
        vBattement: 6 + Math.random() * 6,
        couleur: COULEURS[(Math.random() * COULEURS.length) | 0],
        solDepasse: h + 60,
      });
    }
  }

  function etape(maintenant) {
    // Un onglet mis en veille rend un dt énorme : le borner évite que tous les
    // morceaux traversent l'écran d'un coup au retour.
    const dt = Math.min(0.05, (maintenant - precedent) / 1000 || 0);
    precedent = maintenant;

    const { l, h } = ajuster();
    ctx.clearRect(0, 0, l, h);

    const reste = Math.max(0, echeance - maintenant);
    const opacite = Math.min(1, reste / 700);
    const amorti = Math.pow(FROTTEMENT, dt);

    let vivants = 0;
    for (const m of morceaux) {
      m.vx *= amorti;
      m.vy = m.vy * amorti + GRAVITE * dt;
      m.x += m.vx * dt;
      m.y += m.vy * dt;
      m.angle += m.vAngle * dt;
      m.battement += m.vBattement * dt;

      if (m.y > m.solDepasse) continue;
      vivants += 1;

      ctx.save();
      ctx.globalAlpha = opacite;
      ctx.translate(m.x, m.y);
      ctx.rotate(m.angle);
      // Le facteur d'échelle horizontal oscille : le rectangle se présente
      // tantôt de face, tantôt de profil.
      ctx.scale(Math.cos(m.battement), 1);
      ctx.fillStyle = m.couleur;
      ctx.fillRect(-m.w / 2, -m.h / 2, m.w, m.h);
      ctx.restore();
    }

    if (vivants > 0 && maintenant < echeance) {
      frame = requestAnimationFrame(etape);
    } else {
      arreter();
    }
  }

  function arreter() {
    if (frame !== null) {
      cancelAnimationFrame(frame);
      frame = null;
    }
    morceaux = [];
    const { l, h } = ajuster();
    ctx.clearRect(0, 0, l, h);
  }

  return {
    /**
     * Tire une salve.
     * @param {{nombre?: number, duree?: number}} options
     */
    lancer(options = {}) {
      const { nombre = 150, duree = 3200 } = options;
      const { l, h } = ajuster();

      morceaux = [];
      // Deux canons, depuis les coins bas, tirant vers le haut et l'intérieur.
      canon(l * 0.06, h * 1.02, -Math.PI / 2.55, Math.ceil(nombre / 2), l, h);
      canon(l * 0.94, h * 1.02, -Math.PI + Math.PI / 2.55, Math.floor(nombre / 2), l, h);

      precedent = performance.now();
      echeance = precedent + duree;
      if (frame !== null) cancelAnimationFrame(frame);
      frame = requestAnimationFrame(etape);
    },
    arreter,
  };
}
