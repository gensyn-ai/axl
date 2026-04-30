// Bootstrap + SSE client + panel renderers.
(function () {
  const state = {
    snapshot: null,
    counters: null,
    rates: { msg: 0, byte: 0 },
    lastCounters: null,
    lastCountersAt: 0,
    logs: [],
    paused: false,
    logFilter: '',
  };

  const charts = {
    msgs:  new LineChart(document.getElementById('chart-msgs'),  { color: '#f5a524', fill: 'rgba(245,165,36,0.10)' }),
    bytes: new LineChart(document.getElementById('chart-bytes'), { color: '#58a6ff', fill: 'rgba(88,166,255,0.10)' }),
    conns: new LineChart(document.getElementById('chart-conns'), { color: '#2ea043', fill: 'rgba(46,160,67,0.10)' }),
    queue: new LineChart(document.getElementById('chart-queue'), { color: '#d29922', fill: 'rgba(210,153,34,0.10)' }),
  };

  const topo = new TopologyView(document.getElementById('topo-svg'), { mode: 'tree' });

  // ---- transport: SSE with polling fallback ----
  let es = null;
  function connectSSE() {
    setStatus('connecting');
    es = new EventSource('/dashboard/api/events');
    es.addEventListener('open', () => setStatus('live'));
    es.addEventListener('snapshot', (e) => { try { handleSnapshot(JSON.parse(e.data)); } catch (err) { console.error(err); } });
    es.addEventListener('counters', (e) => { try { handleCounters(JSON.parse(e.data)); } catch (err) { console.error(err); } });
    es.addEventListener('peers',    (e) => { try { handlePeersEvent(JSON.parse(e.data)); } catch (err) { console.error(err); } });
    es.addEventListener('log',      (e) => { try { handleLogEvent(JSON.parse(e.data)); } catch (err) { console.error(err); } });
    es.addEventListener('error', () => {
      setStatus('poll');
      es.close();
      es = null;
      pollOnce();
      setInterval(pollOnce, 2000);
    });
  }

  async function pollOnce() {
    try {
      const r = await fetch('/dashboard/api/snapshot');
      const data = await r.json();
      handleSnapshot(data);
      const lr = await fetch('/dashboard/api/logs?limit=200');
      const logs = await lr.json();
      state.logs = logs || [];
      renderLogs();
    } catch (err) {
      setStatus('down');
    }
  }

  // ---- handlers ----
  function handleSnapshot(s) {
    state.snapshot = s;
    state.counters = s.metrics;
    renderIdentity(s);
    renderUptime(s.uptime_secs);
    renderRuntime(s.metrics);
    renderForwarders(s.metrics);
    renderTcp(s.metrics);
    renderPeers(s.peers || []);
    renderTopology(s.topology, s.peers || []);
    renderSeries(s.series || []);
    renderHeadlineRates(s.metrics);
  }

  function handleCounters(c) {
    state.counters = { counters: c.counters, gauges: c.gauges };
    renderUptime(c.uptime_secs);
    // streaming: append latest deltas to charts
    appendCountersToCharts(c);
    renderRuntime({ counters: c.counters, gauges: c.gauges });
    renderForwarders({ counters: c.counters, gauges: c.gauges });
    renderTcp({ counters: c.counters, gauges: c.gauges });
    renderHeadlineRates({ counters: c.counters, gauges: c.gauges });
  }

  function handlePeersEvent(t) {
    state.snapshot = state.snapshot || {};
    state.snapshot.topology = t;
    renderTopology(t, state.snapshot.peers || []);
  }

  function handleLogEvent(line) {
    if (state.paused) return;
    state.logs.push(line);
    if (state.logs.length > 500) state.logs.shift();
    renderLogs();
  }

  // ---- chart streaming ----
  function appendCountersToCharts(c) {
    const now = c.at ? new Date(c.at).getTime() : Date.now();
    const cnt = c.counters || {};
    const gau = c.gauges || {};
    if (state.lastCounters && state.lastCountersAt) {
      const dt = (now - state.lastCountersAt) / 1000;
      if (dt > 0 && dt < 60) {
        const msgRate  = ((cnt.messages_in_total || 0) - (state.lastCounters.messages_in_total || 0)) / dt;
        const byteRate = ((cnt.message_bytes_in_total || 0) - (state.lastCounters.message_bytes_in_total || 0)) / dt;
        charts.msgs.push(Math.max(0, msgRate));
        charts.bytes.push(Math.max(0, byteRate));
        state.rates.msg = msgRate;
        state.rates.byte = byteRate;
      }
    }
    charts.conns.push(gau.tcp_active_conns || 0);
    charts.queue.push(gau.recv_queue_depth || 0);
    state.lastCounters = { ...cnt };
    state.lastCountersAt = now;
  }

  function renderHeadlineRates(metrics) {
    const cnt = metrics.counters || {};
    const gau = metrics.gauges || {};
    setStat('msg_rate',     fmt.count(state.rates.msg) + '/s');
    setStat('byte_rate',    fmt.rate(state.rates.byte));
    setStat('active_conns', fmt.num(gau.tcp_active_conns || 0));
    setStat('recv_depth',   fmt.num(gau.recv_queue_depth || 0));
  }

  function renderSeries(series) {
    const find = (n) => (series.find(s => s.name === n) || { values: [] }).values;
    if (find('messages_in_per_sec').length) charts.msgs.setValues(find('messages_in_per_sec'));
    if (find('message_bytes_in_per_sec').length) charts.bytes.setValues(find('message_bytes_in_per_sec'));
    if (find('tcp_active_conns').length) charts.conns.setValues(find('tcp_active_conns'));
    if (find('recv_queue_depth').length) charts.queue.setValues(find('recv_queue_depth'));
  }

  // ---- render: identity ----
  function renderIdentity(s) {
    const id = s.identity || {};
    setIdent('ipv6', id.ipv6 || '—');
    setIdent('key',  id.public_key ? fmt.shortKey(id.public_key, 12) : '—');
    setIdent('host', id.hostname || '—');
  }
  function setIdent(k, v) {
    const el = document.querySelector(`#ident-${k} .ident-val`);
    if (el) el.textContent = v;
  }

  function renderUptime(secs) {
    const el = document.getElementById('uptime');
    if (el) el.textContent = fmt.duration(secs);
  }

  // ---- render: peers table ----
  function renderPeers(peerStats) {
    const tbody = document.querySelector('#peer-table tbody');
    const peers = (state.snapshot && state.snapshot.topology && state.snapshot.topology.peers) || [];
    const peerById = new Map(peerStats.map(p => [p.peer_id, p]));
    const rows = peers.length ? peers : peerStats.map(p => ({ public_key: p.peer_id, up: p.active_conns > 0, inbound: false, uri: '' }));
    document.getElementById('peer-count').textContent = `${rows.length} peer${rows.length === 1 ? '' : 's'}`;
    tbody.innerHTML = '';
    for (const p of rows) {
      const stat = peerById.get(p.public_key) || {};
      const tr = document.createElement('tr');
      tr.innerHTML = `
        <td><span class="pkey short">${escapeHtml(p.public_key.slice(0, 16))}</span><span class="pkey">${escapeHtml(p.public_key.slice(16))}</span></td>
        <td class="peer-dir">${p.inbound ? 'in' : 'out'}</td>
        <td class="${p.up ? 'peer-up' : 'peer-down'}">${p.up ? '●' : '○'}</td>
        <td class="num">${fmt.bytes(stat.bytes_in || 0)}</td>
        <td class="num">${fmt.bytes(stat.bytes_out || 0)}</td>
        <td class="num">${fmt.count(stat.messages_in || 0)}</td>
        <td class="num">${fmt.count(stat.messages_out || 0)}</td>
        <td class="num">${stat.active_conns || 0}</td>
        <td class="num">${stat.last_seen ? fmt.relTime(stat.last_seen) + ' ago' : '—'}</td>
      `;
      tbody.appendChild(tr);
    }
  }

  // ---- render: topology ----
  function renderTopology(t, peerStats) {
    if (!t) return;
    topo.setData({ self: t.self, peers: t.peers || [], tree: t.tree || [], peerStats });
    const stats = document.getElementById('topo-stats');
    const direct = (t.peers || []).length;
    const total = (t.tree || []).length || direct + 1;
    stats.textContent = `${total} node${total !== 1 ? 's' : ''} in mesh · ${direct} direct peer${direct !== 1 ? 's' : ''}`;
  }

  // ---- render: kv panels ----
  function renderRuntime(metrics) {
    const g = metrics.gauges || {};
    const kv = [
      ['goroutines',   fmt.num(g.runtime_goroutines)],
      ['heap',         fmt.bytes(g.runtime_heap_bytes)],
      ['sys',          fmt.bytes(g.runtime_sys_bytes)],
      ['stack',        fmt.bytes(g.runtime_stack_bytes)],
      ['gc count',     fmt.num(g.runtime_gc_count)],
      ['gc pause',     fmt.ms((g.runtime_gc_pause_last_ns || 0) / 1e6)],
    ];
    renderKV('runtime-kv', kv);
  }

  function renderForwarders(metrics) {
    const c = metrics.counters || {};
    const kv = [
      ['mcp requests', fmt.num(c.mcp_forward_requests_total || 0)],
      ['mcp errors',   fmt.num(c.mcp_forward_errors_total || 0)],
      ['a2a requests', fmt.num(c.a2a_forward_requests_total || 0)],
      ['a2a errors',   fmt.num(c.a2a_forward_errors_total || 0)],
      ['send requests', fmt.num(c.send_requests_total || 0)],
      ['send errors',  fmt.num(c.send_errors_total || 0)],
    ];
    renderKV('forwarders-kv', kv);
  }

  function renderTcp(metrics) {
    const c = metrics.counters || {};
    const g = metrics.gauges || {};
    const kv = [
      ['accepts',     fmt.num(c.tcp_accepts_total || 0)],
      ['rejects',     fmt.num(c.tcp_rejects_total || 0)],
      ['active',      fmt.num(g.tcp_active_conns || 0)],
      ['oversize',    fmt.num(c.tcp_oversize_drops_total || 0)],
      ['read err',    fmt.num(c.tcp_read_errors_total || 0)],
      ['accept err',  fmt.num(c.tcp_accept_errors_total || 0)],
      ['msgs in',     fmt.count(c.messages_in_total || 0)],
      ['bytes in',    fmt.bytes(c.message_bytes_in_total || 0)],
      ['msgs out',    fmt.count(c.messages_out_total || 0)],
      ['bytes out',   fmt.bytes(c.message_bytes_out_total || 0)],
      ['queue depth', fmt.num(g.recv_queue_depth || 0)],
      ['queue drops', fmt.num(c.recv_queue_drops_total || 0)],
    ];
    renderKV('tcp-kv', kv);
  }

  function renderKV(targetId, kv) {
    const el = document.getElementById(targetId);
    if (!el) return;
    let html = '';
    for (const [k, v] of kv) {
      const warn = (k.includes('error') || k.includes('drop') || k.includes('reject')) && v !== '0' && v !== '—';
      html += `<div class="kv"><span class="k">${escapeHtml(k)}</span><span class="v ${warn ? 'warn' : ''}">${escapeHtml(v)}</span></div>`;
    }
    el.innerHTML = html;
  }

  // ---- render: logs ----
  function renderLogs() {
    const body = document.getElementById('log-body');
    const filt = state.logFilter.toLowerCase();
    const html = state.logs
      .filter(l => !filt || l.text.toLowerCase().includes(filt))
      .map(l => {
        const t = new Date(l.at);
        const ts = t.toTimeString().slice(0, 8);
        return `<span class="log-line level-${escapeHtml(l.level)}"><span class="ts">${ts}</span><span class="text">${escapeHtml(l.text)}</span></span>`;
      })
      .join('\n');
    body.innerHTML = html;
    body.scrollTop = body.scrollHeight;
  }

  // ---- helpers ----
  function setStatus(state) {
    document.getElementById('status-dot').dataset.state = state;
    document.getElementById('status-label').textContent = state;
  }
  function setStat(name, val) {
    const el = document.querySelector(`[data-stat="${name}"]`);
    if (el) el.textContent = val;
  }
  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  }

  // ---- controls ----
  document.getElementById('topo-mode-tree').addEventListener('click', () => {
    document.getElementById('topo-mode-tree').dataset.active = 'true';
    document.getElementById('topo-mode-force').dataset.active = 'false';
    topo.setMode('tree');
  });
  document.getElementById('topo-mode-force').addEventListener('click', () => {
    document.getElementById('topo-mode-tree').dataset.active = 'false';
    document.getElementById('topo-mode-force').dataset.active = 'true';
    topo.setMode('force');
  });
  document.getElementById('topo-freeze').addEventListener('click', (e) => {
    const next = e.target.dataset.active !== 'true';
    e.target.dataset.active = next ? 'true' : 'false';
    e.target.textContent = next ? 'frozen' : 'freeze';
    topo.setFreeze(next);
  });
  document.getElementById('topo-search').addEventListener('input', (e) => {
    topo.setFilter(e.target.value);
  });
  document.getElementById('log-filter').addEventListener('input', (e) => {
    state.logFilter = e.target.value;
    renderLogs();
  });
  document.getElementById('log-pause').addEventListener('click', (e) => {
    state.paused = !state.paused;
    e.target.dataset.active = state.paused ? 'true' : 'false';
    e.target.textContent = state.paused ? 'paused' : 'pause';
  });
  document.getElementById('log-clear').addEventListener('click', () => {
    state.logs = [];
    renderLogs();
  });

  // ---- bootstrap ----
  pollOnce().then(() => connectSSE());
})();
