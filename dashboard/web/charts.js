// Minimal canvas line charts. Streaming-friendly: append a value, redraw.
class LineChart {
  constructor(canvas, opts = {}) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.values = [];
    this.maxLen = opts.maxLen || 300;
    this.color = opts.color || '#f5a524';
    this.fill = opts.fill || 'rgba(245, 165, 36, 0.10)';
    this.gridColor = opts.gridColor || '#1f262e';
    this.dpr = window.devicePixelRatio || 1;
    this.resize();
    window.addEventListener('resize', () => this.resize());
  }
  resize() {
    const r = this.canvas.getBoundingClientRect();
    this.w = Math.max(2, r.width);
    this.h = Math.max(2, r.height);
    this.canvas.width = this.w * this.dpr;
    this.canvas.height = this.h * this.dpr;
    this.ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    this.draw();
  }
  setValues(arr) {
    this.values = arr.slice(-this.maxLen);
    this.draw();
  }
  push(v) {
    this.values.push(v);
    if (this.values.length > this.maxLen) this.values.shift();
    this.draw();
  }
  draw() {
    const ctx = this.ctx;
    const { w, h, values } = this;
    ctx.clearRect(0, 0, w, h);

    if (values.length === 0) {
      ctx.fillStyle = '#3a4452';
      ctx.font = '10px ui-monospace, monospace';
      ctx.fillText('no data', 4, 14);
      return;
    }

    let min = Infinity, max = -Infinity;
    for (const v of values) {
      if (v == null || isNaN(v)) continue;
      if (v < min) min = v;
      if (v > max) max = v;
    }
    if (!isFinite(min)) { min = 0; max = 1; }
    if (max === min) { max = min + 1; }
    if (min > 0) min = 0; // anchor to zero for rate-like metrics

    // grid
    ctx.strokeStyle = this.gridColor;
    ctx.lineWidth = 1;
    for (let i = 1; i < 4; i++) {
      const y = (h * i) / 4;
      ctx.beginPath();
      ctx.moveTo(0, y);
      ctx.lineTo(w, y);
      ctx.stroke();
    }

    // y-axis label (max)
    ctx.fillStyle = '#4a5563';
    ctx.font = '9px ui-monospace, monospace';
    ctx.fillText(this._fmtMax(max), 2, 10);

    // path
    const n = values.length;
    const stepX = w / Math.max(1, this.maxLen - 1);
    const startX = w - stepX * (n - 1);
    const yScale = (h - 4) / (max - min);

    ctx.beginPath();
    for (let i = 0; i < n; i++) {
      const x = startX + i * stepX;
      const v = values[i] == null ? 0 : values[i];
      const y = h - (v - min) * yScale - 1;
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    // fill
    ctx.lineTo(startX + (n - 1) * stepX, h);
    ctx.lineTo(startX, h);
    ctx.closePath();
    ctx.fillStyle = this.fill;
    ctx.fill();

    // stroke
    ctx.beginPath();
    for (let i = 0; i < n; i++) {
      const x = startX + i * stepX;
      const v = values[i] == null ? 0 : values[i];
      const y = h - (v - min) * yScale - 1;
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.strokeStyle = this.color;
    ctx.lineWidth = 1.4;
    ctx.stroke();

    // last value dot
    if (n > 0) {
      const last = values[n - 1] || 0;
      const x = startX + (n - 1) * stepX;
      const y = h - (last - min) * yScale - 1;
      ctx.fillStyle = this.color;
      ctx.beginPath();
      ctx.arc(x, y, 2, 0, Math.PI * 2);
      ctx.fill();
    }
  }
  _fmtMax(v) {
    if (v >= 1e6) return `${(v/1e6).toFixed(1)}M`;
    if (v >= 1e3) return `${(v/1e3).toFixed(1)}k`;
    if (v >= 10) return `${v.toFixed(0)}`;
    return v.toFixed(2);
  }
}
