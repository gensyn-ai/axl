// Force-directed + tidy-tree topology renderer. Pure SVG, no deps.
class TopologyView {
  constructor(svg, opts = {}) {
    this.svg = svg;
    this.mode = opts.mode || 'tree';
    this.frozen = false;
    this.filter = '';
    this.nodes = []; // {id, kind, x, y, vx, vy, fx, fy, label}
    this.edges = []; // {a, b, kind}
    this.selfId = null;
    this.tooltip = document.createElement('div');
    this.tooltip.className = 'topo-tip';
    document.body.appendChild(this.tooltip);
    this._wireZoom();
    this._raf = null;
    this._tick = this._tick.bind(this);
    this.zoom = { k: 1, x: 0, y: 0 };
  }

  setData({ self, peers = [], tree = [], peerStats = [] }) {
    this.selfId = self || null;
    const peerStatMap = new Map();
    for (const p of peerStats) peerStatMap.set(p.peer_id, p);

    // Build node set
    const nodes = new Map();
    if (self) {
      nodes.set(self, { id: self, kind: 'self', label: self });
    }
    for (const p of peers) {
      if (!nodes.has(p.public_key)) {
        nodes.set(p.public_key, { id: p.public_key, kind: 'peer', label: p.public_key, peer: p });
      } else {
        nodes.get(p.public_key).peer = p;
      }
    }
    for (const t of tree) {
      if (!nodes.has(t.public_key)) {
        nodes.set(t.public_key, { id: t.public_key, kind: 'tree', label: t.public_key, tree: t });
      } else {
        nodes.get(t.public_key).tree = t;
      }
    }

    // Edges
    const edges = [];
    // direct peer links from self
    if (self) {
      for (const p of peers) {
        edges.push({ a: self, b: p.public_key, kind: 'direct', up: p.up });
      }
    }
    // tree parent edges
    for (const t of tree) {
      if (t.parent && t.parent !== '0000000000000000000000000000000000000000000000000000000000000000') {
        edges.push({ a: t.parent, b: t.public_key, kind: 'tree' });
      }
    }

    // Preserve existing positions where possible
    const old = new Map(this.nodes.map(n => [n.id, n]));
    const arr = [];
    for (const n of nodes.values()) {
      const prev = old.get(n.id);
      if (prev) {
        n.x = prev.x; n.y = prev.y; n.vx = prev.vx || 0; n.vy = prev.vy || 0;
      } else {
        n.vx = 0; n.vy = 0;
      }
      n.peerStat = peerStatMap.get(n.id) || null;
      arr.push(n);
    }
    this.nodes = arr;
    this.edges = edges;
    this._initLayout();
    this.draw();
  }

  setMode(mode) { this.mode = mode; this._initLayout(); this.draw(); }
  setFreeze(b)  { this.frozen = b; if (!b && this.mode === 'force') this._loop(); }
  setFilter(s)  { this.filter = (s || '').toLowerCase(); this.draw(); }

  _initLayout() {
    const { width, height } = this._dims();
    const cx = width / 2, cy = height / 2;
    if (this.mode === 'tree') {
      this._tidyTree(cx, cy, width, height);
    } else {
      // force: place anything without coords on a ring
      let unplaced = this.nodes.filter(n => !n.x);
      const r = Math.min(width, height) * 0.32;
      unplaced.forEach((n, i) => {
        const a = (i / Math.max(1, unplaced.length)) * Math.PI * 2;
        n.x = cx + Math.cos(a) * r;
        n.y = cy + Math.sin(a) * r;
      });
      const selfNode = this.nodes.find(n => n.id === this.selfId);
      if (selfNode) { selfNode.x = cx; selfNode.y = cy; selfNode.fx = cx; selfNode.fy = cy; }
      this._loop();
    }
  }

  _tidyTree(cx, cy, W, H) {
    if (!this.selfId) return;
    // Build child map from tree edges only (parent → children)
    const childMap = new Map();
    for (const e of this.edges) {
      if (e.kind !== 'tree') continue;
      if (!childMap.has(e.a)) childMap.set(e.a, []);
      childMap.get(e.a).push(e.b);
    }
    // Find root: a tree node with no incoming tree edge.
    const inSet = new Set();
    for (const e of this.edges) if (e.kind === 'tree') inSet.add(e.b);
    let root = null;
    for (const n of this.nodes) {
      if (!inSet.has(n.id)) { root = n.id; break; }
    }
    if (!root) root = this.selfId;

    // BFS depth
    const depth = new Map();
    depth.set(root, 0);
    const q = [root];
    let maxDepth = 0;
    while (q.length) {
      const id = q.shift();
      const d = depth.get(id);
      maxDepth = Math.max(maxDepth, d);
      const kids = childMap.get(id) || [];
      for (const k of kids) {
        if (!depth.has(k)) { depth.set(k, d + 1); q.push(k); }
      }
    }
    // Group by depth
    const byDepth = new Map();
    for (const n of this.nodes) {
      const d = depth.get(n.id);
      if (d == null) continue;
      if (!byDepth.has(d)) byDepth.set(d, []);
      byDepth.get(d).push(n);
    }
    // unreached nodes (no tree path) — bucket them at the bottom
    const reached = new Set([...depth.keys()]);
    const orphans = this.nodes.filter(n => !reached.has(n.id));
    if (orphans.length) byDepth.set(maxDepth + 1, orphans);

    const levels = Math.max(1, byDepth.size);
    const padTop = 40, padBottom = 40, padX = 40;
    const usableH = Math.max(40, H - padTop - padBottom);
    const usableW = Math.max(40, W - padX * 2);
    [...byDepth.entries()].sort((a, b) => a[0] - b[0]).forEach(([d, group]) => {
      const y = padTop + (usableH * d) / Math.max(1, levels - 1 || 1);
      group.sort((a, b) => a.id.localeCompare(b.id));
      group.forEach((n, i) => {
        const x = padX + (usableW * (i + 0.5)) / group.length;
        n.x = x; n.y = y; n.fx = x; n.fy = y;
      });
    });
  }

  _loop() {
    if (this._raf) cancelAnimationFrame(this._raf);
    this._raf = requestAnimationFrame(this._tick);
  }
  _tick() {
    if (this.frozen || this.mode !== 'force') { this._raf = null; return; }
    this._step();
    this.draw();
    this._raf = requestAnimationFrame(this._tick);
  }
  _step() {
    const { width, height } = this._dims();
    const cx = width / 2, cy = height / 2;
    const nodes = this.nodes;
    // Simple force model
    const k = 80; // ideal edge length
    const repulse = 800;
    for (const n of nodes) { n.fxv = 0; n.fyv = 0; }
    // Repulsion
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j];
        let dx = a.x - b.x, dy = a.y - b.y;
        let d2 = dx*dx + dy*dy + 0.01;
        const f = repulse / d2;
        const d = Math.sqrt(d2);
        const ux = dx / d, uy = dy / d;
        a.fxv += ux * f; a.fyv += uy * f;
        b.fxv -= ux * f; b.fyv -= uy * f;
      }
    }
    // Springs
    const idIdx = new Map(nodes.map((n, i) => [n.id, i]));
    for (const e of this.edges) {
      const ai = idIdx.get(e.a), bi = idIdx.get(e.b);
      if (ai == null || bi == null) continue;
      const a = nodes[ai], b = nodes[bi];
      let dx = b.x - a.x, dy = b.y - a.y;
      const d = Math.sqrt(dx*dx + dy*dy) + 0.01;
      const f = (d - k) * 0.06;
      const ux = dx / d, uy = dy / d;
      a.fxv += ux * f; a.fyv += uy * f;
      b.fxv -= ux * f; b.fyv -= uy * f;
    }
    // Center gravity
    for (const n of nodes) {
      n.fxv += (cx - n.x) * 0.005;
      n.fyv += (cy - n.y) * 0.005;
    }
    // Integrate
    for (const n of nodes) {
      if (n.fx != null) { n.x = n.fx; n.y = n.fy; continue; }
      n.vx = (n.vx + n.fxv) * 0.7;
      n.vy = (n.vy + n.fyv) * 0.7;
      n.x += Math.max(-12, Math.min(12, n.vx));
      n.y += Math.max(-12, Math.min(12, n.vy));
      n.x = Math.max(20, Math.min(width - 20, n.x));
      n.y = Math.max(20, Math.min(height - 20, n.y));
    }
  }

  _dims() {
    const r = this.svg.getBoundingClientRect();
    return { width: r.width, height: r.height };
  }

  draw() {
    const svg = this.svg;
    while (svg.firstChild) svg.removeChild(svg.firstChild);
    const { width, height } = this._dims();
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);

    const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
    g.setAttribute('transform', `translate(${this.zoom.x}, ${this.zoom.y}) scale(${this.zoom.k})`);
    svg.appendChild(g);

    const filtered = this.filter ? new Set(this.nodes.filter(n => n.id.toLowerCase().includes(this.filter)).map(n => n.id)) : null;

    // Edges first
    const idMap = new Map(this.nodes.map(n => [n.id, n]));
    for (const e of this.edges) {
      const a = idMap.get(e.a), b = idMap.get(e.b);
      if (!a || !b) continue;
      if (filtered && !filtered.has(e.a) && !filtered.has(e.b)) continue;
      const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
      line.setAttribute('x1', a.x); line.setAttribute('y1', a.y);
      line.setAttribute('x2', b.x); line.setAttribute('y2', b.y);
      line.setAttribute('class', `edge ${e.kind}`);
      g.appendChild(line);
    }

    // Nodes
    for (const n of this.nodes) {
      if (filtered && !filtered.has(n.id)) continue;
      const c = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
      c.setAttribute('cx', n.x);
      c.setAttribute('cy', n.y);
      let r = 5;
      if (n.kind === 'self') r = 9;
      else if (n.kind === 'peer') r = 7;
      c.setAttribute('r', r);
      let cls = 'node';
      if (n.kind === 'self') cls += ' node-self';
      else if (n.kind === 'peer') cls += ' node-peer' + (n.peer && !n.peer.up ? ' down' : '');
      else cls += ' node-tree';
      c.setAttribute('class', cls);
      c.addEventListener('mouseenter', (e) => this._showTip(e, n));
      c.addEventListener('mousemove', (e) => this._moveTip(e));
      c.addEventListener('mouseleave', () => this._hideTip());
      g.appendChild(c);

      // Labels for self + peers (tree nodes get a label only on hover)
      if (n.kind !== 'tree') {
        const t = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        t.setAttribute('x', n.x + 10);
        t.setAttribute('y', n.y + 3);
        t.setAttribute('class', 'node-label' + (n.kind === 'self' ? ' self' : ''));
        t.textContent = n.id.slice(0, 12);
        g.appendChild(t);
      }
    }
  }

  _showTip(e, n) {
    const lines = [`<div><span class="k">id</span>${n.id.slice(0, 16)}…</div>`];
    if (n.kind === 'self') lines.push(`<div><span class="k">role</span>this node</div>`);
    if (n.peer) {
      lines.push(`<div><span class="k">uri</span>${escapeHtml(n.peer.uri || '—')}</div>`);
      lines.push(`<div><span class="k">dir</span>${n.peer.inbound ? 'inbound' : 'outbound'}</div>`);
      lines.push(`<div><span class="k">up</span>${n.peer.up ? 'yes' : 'no'}</div>`);
      if (n.peer.coords && n.peer.coords.length) {
        lines.push(`<div><span class="k">coords</span>[${n.peer.coords.join(', ')}]</div>`);
      }
    }
    if (n.peerStat) {
      lines.push(`<div><span class="k">in</span>${fmt.bytes(n.peerStat.bytes_in)} · ${fmt.count(n.peerStat.messages_in)} msgs</div>`);
      lines.push(`<div><span class="k">out</span>${fmt.bytes(n.peerStat.bytes_out)} · ${fmt.count(n.peerStat.messages_out)} msgs</div>`);
    }
    if (n.tree) lines.push(`<div><span class="k">seq</span>${n.tree.sequence}</div>`);
    this.tooltip.innerHTML = lines.join('');
    this.tooltip.classList.add('visible');
    this._moveTip(e);
  }
  _moveTip(e) {
    this.tooltip.style.left = (e.clientX + 12) + 'px';
    this.tooltip.style.top  = (e.clientY + 12) + 'px';
  }
  _hideTip() { this.tooltip.classList.remove('visible'); }

  _wireZoom() {
    let dragging = false, lastX = 0, lastY = 0;
    this.svg.addEventListener('wheel', (e) => {
      e.preventDefault();
      const factor = e.deltaY < 0 ? 1.1 : 1 / 1.1;
      const r = this.svg.getBoundingClientRect();
      const mx = e.clientX - r.left, my = e.clientY - r.top;
      this.zoom.x = mx - (mx - this.zoom.x) * factor;
      this.zoom.y = my - (my - this.zoom.y) * factor;
      this.zoom.k *= factor;
      this.zoom.k = Math.max(0.2, Math.min(5, this.zoom.k));
      this.draw();
    }, { passive: false });
    this.svg.addEventListener('mousedown', (e) => {
      dragging = true; lastX = e.clientX; lastY = e.clientY;
    });
    window.addEventListener('mousemove', (e) => {
      if (!dragging) return;
      this.zoom.x += e.clientX - lastX;
      this.zoom.y += e.clientY - lastY;
      lastX = e.clientX; lastY = e.clientY;
      this.draw();
    });
    window.addEventListener('mouseup', () => { dragging = false; });
  }
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}
