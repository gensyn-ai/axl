// Small humanizer helpers. No deps.
const fmt = (() => {
  function bytes(n) {
    if (n == null || isNaN(n)) return '—';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let u = 0;
    let v = +n;
    while (v >= 1024 && u < units.length - 1) {
      v /= 1024;
      u++;
    }
    if (u === 0) return `${v|0} ${units[u]}`;
    return `${v.toFixed(v < 10 ? 2 : 1)} ${units[u]}`;
  }
  function rate(n) {
    return bytes(n) + '/s';
  }
  function count(n) {
    if (n == null || isNaN(n)) return '—';
    if (n < 1000) return `${n}`;
    if (n < 1e6) return `${(n/1e3).toFixed(n < 10000 ? 2 : 1)}k`;
    if (n < 1e9) return `${(n/1e6).toFixed(2)}M`;
    return `${(n/1e9).toFixed(2)}G`;
  }
  function num(n) {
    if (n == null || isNaN(n)) return '—';
    return Number(n).toLocaleString();
  }
  function pct(n) {
    if (n == null || isNaN(n)) return '—';
    return `${(n * 100).toFixed(1)}%`;
  }
  function duration(secs) {
    if (secs == null || isNaN(secs)) return '—';
    secs = Math.floor(secs);
    if (secs < 60) return `${secs}s`;
    const m = Math.floor(secs / 60);
    if (m < 60) return `${m}m ${secs % 60}s`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ${m % 60}m`;
    const d = Math.floor(h / 24);
    return `${d}d ${h % 24}h`;
  }
  function shortKey(k, len = 12) {
    if (!k) return '—';
    if (k.length <= len * 2 + 3) return k;
    return `${k.slice(0, len)}…${k.slice(-4)}`;
  }
  function ms(n) {
    if (n == null || isNaN(n)) return '—';
    if (n < 1) return `${(n*1000).toFixed(0)}µs`;
    if (n < 1000) return `${n.toFixed(n < 10 ? 2 : 1)}ms`;
    return `${(n/1000).toFixed(2)}s`;
  }
  function relTime(iso) {
    if (!iso) return 'never';
    const t = typeof iso === 'string' ? new Date(iso).getTime() : iso;
    const diff = (Date.now() - t) / 1000;
    if (diff < 1) return 'now';
    if (diff < 60) return `${diff|0}s`;
    if (diff < 3600) return `${(diff/60)|0}m`;
    if (diff < 86400) return `${(diff/3600)|0}h`;
    return `${(diff/86400)|0}d`;
  }
  return { bytes, rate, count, num, pct, duration, shortKey, ms, relTime };
})();
