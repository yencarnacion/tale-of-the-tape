const $ = selector => document.querySelector(selector);
const money = value => new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format((value || 0) / 1e6);
const dt = value => new Date(typeof value === 'string' ? value : value / 1000).toLocaleString();
const esc = value => String(value ?? '').replace(/[&<>]/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' })[char]);
let view = 'dashboard', selected = null, selectedDay = '', tradePage = 0, tradeSort = 'exit_at', tradeDescending = true;
let dateStart = '', dateEnd = '', datePreset = '', calendarMonth = new Date();
let indicatorState = JSON.parse(localStorage.getItem('tale-indicators') || '{"vwap":true,"sma9":true,"sma20":true,"ema9":true,"ema20":true,"bollinger":true,"volume":true}');

async function api(url, options) {
  const response = await fetch(url, options);
  const body = await response.json();
  if (!response.ok) throw new Error(body.error?.message || 'Request failed');
  return body;
}
function query(extra = '') {
  const parts = [];
  if (dateStart) parts.push(`start=${encodeURIComponent(dateStart)}`);
  if (dateEnd) parts.push(`end=${encodeURIComponent(dateEnd)}`);
  if (extra) parts.push(extra);
  return parts.length ? `?${parts.join('&')}` : '';
}
function filters() {
  const label = dateStart && dateEnd ? `Showing ${datePreset === 'week' ? 'this week' : datePreset === 'month' ? 'month to date' : datePreset === 'year' ? 'year to date' : 'custom range'}: ${dateStart} through ${dateEnd}` : 'Showing all imported dates';
  return `<section class="range-filter"><div class="toolbar"><button class="${datePreset === 'week' ? 'active' : ''}" onclick="setDatePreset('week')">This week</button><button class="${datePreset === 'month' ? 'active' : ''}" onclick="setDatePreset('month')">Month to date</button><button class="${datePreset === 'year' ? 'active' : ''}" onclick="setDatePreset('year')">Year to date</button><label>Start <input id="start" type="date" value="${dateStart}"></label><label>End <input id="end" type="date" value="${dateEnd}"></label><button onclick="applyDates()">Apply</button><button class="${!dateStart && !dateEnd ? 'active' : ''}" onclick="clearDates()">All dates</button>${dateStart && dateEnd ? '<button onclick="enrichRange()">Calculate range MFE / MAE</button>' : ''}</div><p><strong>${label}</strong></p></section>`;
}
const localDate = date => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
function setDatePreset(period) {
  const end = new Date(), start = new Date(end);
  if (period === 'week') start.setDate(end.getDate() - ((end.getDay() + 6) % 7));
  if (period === 'month') start.setDate(1);
  if (period === 'year') { start.setMonth(0); start.setDate(1); }
  dateStart = localDate(start); dateEnd = localDate(end); datePreset = period;
  localStorage.setItem('tale-date-range', JSON.stringify({ start: dateStart, end: dateEnd, preset: datePreset }));
  render();
}
function applyDates() { dateStart = $('#start').value; dateEnd = $('#end').value; datePreset = ''; localStorage.setItem('tale-date-range', JSON.stringify({ start: dateStart, end: dateEnd, preset: '' })); render(); }
function clearDates() { dateStart = ''; dateEnd = ''; datePreset = ''; localStorage.removeItem('tale-date-range'); render(); }
async function enrichRange() {
  const result = await api('/api/v1/enrichment/range' + query(), { method: 'POST' });
  const failures = Object.keys(result.failed || {}).length;
  alert(`MFE / MAE calculated for ${result.completed} of ${result.requested} trades${failures ? `; ${failures} failed` : ''}.`);
  render();
}
function percent(value) { return value == null ? 'N/A' : `${(value * 100).toFixed(1)}%`; }
function decimal(value, digits = 2) { return value == null ? 'N/A' : Number(value).toLocaleString('en-US', { maximumFractionDigits: digits, minimumFractionDigits: digits }); }
function duration(value) {
  if (value == null) return 'N/A';
  if (value < 60) return `${Math.round(value)} minutes`;
  const hours = value / 60;
  return `${hours >= 2 ? 'about ' : ''}${decimal(hours, 1)} hours`;
}
function countRate(count, total) { return `${count} (${total ? percent(count / total) : 'N/A'})`; }
function metric(name, value, className = '') { return `<div class="card"><label>${name}</label><strong class="${className}">${value}</strong></div>`; }
function patterns(items, title) {
  if (!items?.length) return '';
  return `<section class="panel"><h3>${title}</h3><table><thead><tr><th>Pattern</th><th>Trades</th><th>Win rate</th><th>Net P&L</th><th>Avg P&L</th><th>Avg MFE</th><th>Avg MAE</th></tr></thead><tbody>${items.map(item => `<tr><td>${esc(item.name)}</td><td>${item.summary.total_trades}</td><td>${percent(item.summary.win_rate)}</td><td class="${item.summary.net_pnl >= 0 ? 'green' : 'red'}">${money(item.summary.net_pnl * 1e6)}</td><td>${money(item.summary.average_trade * 1e6)}</td><td>${item.summary.average_mfe == null ? '—' : money(item.summary.average_mfe * 1e6)}</td><td>${item.summary.average_mae == null ? '—' : money(item.summary.average_mae * 1e6)}</td></tr>`).join('')}</tbody></table></section>`;
}

async function dashboard() {
  const [summary, equity, breakdown, drawdown] = await Promise.all([
    api('/api/v1/analytics/summary' + query()), api('/api/v1/analytics/equity' + query()), api('/api/v1/analytics/breakdowns' + query()), api('/api/v1/analytics/equity' + query('series=drawdown'))
  ]);
  setTimeout(() => drawEquity(equity, drawdown), 0);
  const broker = summary.broker_ytd == null || (datePreset && datePreset !== 'year') ? '' : `<section class="panel"><h3>Broker year-to-date reconciliation</h3><p><strong class="${summary.broker_ytd >= 0 ? 'green' : 'red'}">${money(summary.broker_ytd * 1e6)}</strong> through ${esc(summary.broker_ytd_date)} · Thinkorswim statement P/L YTD</p><p class="muted">Closed intraday journal P&L below is reconstructed from imported executions. Broker YTD includes carried-position cost basis that execution-only exports cannot reproduce exactly. Broker-reported commissions and fees YTD: ${money(summary.broker_fees_ytd * 1e6)}.</p></section>`;
  const kelly = (value, preliminary) => value == null ? 'Undefined' : `${percent(value)}${preliminary ? ` prelim · ${summary.kelly_sample}/${summary.kelly_minimum_sample}` : ''}`;
  const preliminaryKelly = summary.raw_kelly == null;
  const detailed = [
    ['Total Gain/Loss', money(summary.net_pnl * 1e6)], ['Largest Gain', money(summary.largest_winner * 1e6)], ['Largest Loss', money(summary.largest_loser * 1e6)],
    ['Average Daily Gain/Loss', money(summary.average_daily_pnl * 1e6)], ['Average Daily Volume', Math.round(summary.average_daily_volume || 0).toLocaleString()], ['Average Per-share Gain/Loss', summary.average_per_share == null ? 'N/A' : money(summary.average_per_share * 1e6)],
    ['Average Trade Gain/Loss', money(summary.average_trade * 1e6)], ['Average Winning Trade', money(summary.average_winner * 1e6)], ['Average Losing Trade', money(summary.average_loser * 1e6)],
    ['Total Number of Trades', summary.total_trades], ['Number of Winning Trades', countRate(summary.wins, summary.total_trades)], ['Number of Losing Trades', countRate(summary.losses, summary.total_trades)],
    ['Average Hold Time (scratch trades)', duration(summary.average_scratch_hold_minutes)], ['Average Hold Time (winning trades)', duration(summary.average_winning_hold_minutes)], ['Average Hold Time (losing trades)', duration(summary.average_losing_hold_minutes)],
    ['Number of Scratch Trades', countRate(summary.scratches, summary.total_trades)], ['Max Consecutive Wins', summary.max_win_streak], ['Max Consecutive Losses', summary.max_loss_streak],
    ['Trade P&L Standard Deviation', summary.trade_pnl_standard_deviation == null ? 'N/A' : money(summary.trade_pnl_standard_deviation * 1e6)], ['System Quality Number (SQN)', decimal(summary.system_quality_number)], ['Probability of Random Chance', percent(summary.probability_random_chance)],
    ['Raw Kelly Percentage', kelly(summary.raw_kelly ?? summary.preliminary_raw_kelly, preliminaryKelly)], ['K-Ratio', decimal(summary.k_ratio)], ['Profit Factor', summary.profit_factor ? decimal(summary.profit_factor) : 'N/A'],
    ['Total Commissions', money(summary.commissions * 1e6)], ['Total Fees', money(summary.fees * 1e6)], ['Average Position MAE', summary.average_mae == null ? 'N/A' : money(summary.average_mae * 1e6)],
    ['Average Position MFE', summary.average_mfe == null ? 'N/A' : money(summary.average_mfe * 1e6)]
  ];
  return `${filters()}${broker}<div class="cards">${[
    metric('Net P&L', money(summary.net_pnl * 1e6), summary.net_pnl >= 0 ? 'green' : 'red'), metric('Win rate', percent(summary.win_rate)),
    metric('Trades', summary.total_trades), metric('Avg winner', money(summary.average_winner * 1e6), 'green'), metric('Avg loser', money(summary.average_loser * 1e6), 'red'),
    metric('Profit factor', summary.profit_factor ? summary.profit_factor.toFixed(2) : 'N/A'), metric('Expectancy', money(summary.expectancy * 1e6)),
    metric('Raw Kelly', kelly(summary.raw_kelly ?? summary.preliminary_raw_kelly, preliminaryKelly)), metric('Half Kelly', kelly(summary.half_kelly ?? summary.preliminary_half_kelly, preliminaryKelly)), metric('Max drawdown', money(summary.max_drawdown * 1e6), 'red'), metric('Avg MFE', summary.average_mfe == null ? 'N/A' : money(summary.average_mfe * 1e6), 'green'), metric('Avg MAE', summary.average_mae == null ? 'N/A' : money(summary.average_mae * 1e6), 'red'), metric('Max win streak', summary.max_win_streak), metric('Max loss streak', summary.max_loss_streak)
  ].join('')}</div><section class="panel"><h2>Detailed statistics</h2><table><tbody>${detailed.map(([name, value]) => `<tr><th>${name}</th><td>${value}</td></tr>`).join('')}</tbody></table><p class="muted">SQN requires at least 30 trades. Probability of random chance is the exact two-sided binomial probability for the observed win/loss split. Volume counts shares opened per completed round trip.</p></section><section class="panel"><h2>Closed-trade equity & drawdown</h2><div id="equity-chart" class="small-chart"></div></section>${patterns(breakdown.tag, 'Performance by tag')}${patterns(breakdown.symbol, 'Performance by symbol')}<section class="panel"><h2>Review, not execution</h2><p class="muted">Metrics use net P&L after commissions and fees. Kelly is analytical only and highly sample-sensitive.</p></section>`;
}
function drawEquity(points, drawdown, selector = '#equity-chart') {
  const el = $(selector);
  if (!el || !points?.length) { if (el) el.textContent = 'No closed trades in this range.'; return; }
  const chart = LightweightCharts.createChart(el, { height: 220, layout: { background: { color: '#161b22' }, textColor: '#c9d1d9' }, grid: { vertLines: { color: '#21262d' }, horzLines: { color: '#21262d' } } });
  const line = chart.addSeries(LightweightCharts.LineSeries, { color: '#58a6ff', lineWidth: 2 });
  line.setData(points); if (drawdown?.length) { const risk = chart.addSeries(LightweightCharts.LineSeries, { color: '#f85149', lineWidth: 1, lastValueVisible: false }); risk.setData(drawdown); } chart.timeScale().fitContent();
}

async function trades() {
  const [rows, tags] = await Promise.all([api('/api/v1/trades' + query()), api('/api/v1/tags')]);
  const sorted = [...rows].sort((a, b) => { const av = a[tradeSort] ?? '', bv = b[tradeSort] ?? ''; const order = typeof av === 'string' ? String(av).localeCompare(String(bv)) : av - bv; return tradeDescending ? -order : order; });
  const pages = Math.max(1, Math.ceil(sorted.length / 50)); if (tradePage >= pages) tradePage = pages - 1; const pageRows = sorted.slice(tradePage * 50, tradePage * 50 + 50);
  return `${filters()}<div class="panel"><div class="toolbar"><label>Sort <select onchange="changeTradeSort(this.value)"><option value="exit_at" ${tradeSort === 'exit_at' ? 'selected' : ''}>Exit time</option><option value="symbol" ${tradeSort === 'symbol' ? 'selected' : ''}>Symbol</option><option value="direction" ${tradeSort === 'direction' ? 'selected' : ''}>Direction</option><option value="net" ${tradeSort === 'net' ? 'selected' : ''}>Net P&L</option><option value="max_quantity" ${tradeSort === 'max_quantity' ? 'selected' : ''}>Max size</option></select></label><button onclick="tradeDescending=!tradeDescending;render()">${tradeDescending ? '↓ Descending' : '↑ Ascending'}</button><label>Bulk tags <select id="bulk-tags" multiple>${tags.filter(tag => !tag.archived).map(tag => `<option value="${tag.id}">${esc(tag.name)}</option>`).join('')}</select></label><button onclick="bulkTag('add')">Add</button><button onclick="bulkTag('remove')">Remove</button><button onclick="bulkTag('set')">Replace</button></div><table><thead><tr><th><input type="checkbox" onchange="selectAllTrades(this.checked)"></th><th>Exit</th><th>Symbol</th><th>Direction</th><th>Duration</th><th>Max size</th><th>Net P&L</th><th>MFE</th><th>MAE</th><th>Source</th><th>Tags</th><th>Note</th></tr></thead><tbody>${pageRows.map(t => `<tr class="clickable" onclick="showTrade(${t.id})"><td><input class="bulk-trade" type="checkbox" value="${t.id}" onclick="event.stopPropagation()"></td><td>${dt(t.exit_at)}</td><td>${esc(t.symbol)}</td><td>${t.direction}</td><td>${Math.round((t.exit_at - t.entry_at) / 6e7)}m</td><td>${t.max_quantity}</td><td class="${t.net >= 0 ? 'green' : 'red'}">${money(t.net)}</td><td>${t.excursion ? money(t.excursion.mfe) : '—'}</td><td>${t.excursion ? money(t.excursion.mae) : '—'}</td><td>${esc(t.excursion ? `${t.excursion.source} · ${t.excursion.completeness}` : 'Not calculated')}</td><td>${t.tags.map(tag => esc(tag.name)).join(', ')}</td><td>${t.note ? '●' : ''}</td></tr>`).join('')}</tbody></table>${rows.length ? `<div class="toolbar"><button onclick="changeTradePage(-1)" ${tradePage ? '' : 'disabled'}>← Previous</button><span class="muted">Page ${tradePage + 1} of ${pages} · ${rows.length} trades</span><button onclick="changeTradePage(1)" ${tradePage + 1 < pages ? '' : 'disabled'}>Next →</button></div>` : '<p class="muted">No completed trades in this range.</p>'}</div>`;
}
function changeTradeSort(value) { tradeSort = value; tradePage = 0; render(); }
function changeTradePage(delta) { tradePage = Math.max(0, tradePage + delta); render(); }
function selectAllTrades(checked) { document.querySelectorAll('.bulk-trade').forEach(input => input.checked = checked); }
async function bulkTag(mode) { const tradeIds = [...document.querySelectorAll('.bulk-trade:checked')].map(input => Number(input.value)); const tagIds = [...$('#bulk-tags').selectedOptions].map(option => Number(option.value)); if (!tradeIds.length || (!tagIds.length && mode !== 'set')) { alert('Select at least one trade and tag.'); return; } await api('/api/v1/trades/bulk-tags', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ trade_ids: tradeIds, tag_ids: tagIds, mode }) }); render(); }
function navigate(route) {
  const target = `#${route}`;
  if (location.hash === target) { applyRoute(); render(); }
  else location.hash = target;
}
function applyRoute() {
  const [route, value] = location.hash.slice(1).split('/');
  if (route === 'trade' && /^\d+$/.test(value || '')) {
    selected = Number(value); view = 'detail'; return;
  }
  if (route === 'day' && /^\d{4}-\d{2}-\d{2}$/.test(value || '')) {
    selectedDay = value; view = 'day'; return;
  }
  view = ['dashboard', 'calendar', 'trades', 'import', 'settings'].includes(route) ? route : 'dashboard';
}
function showTrade(id) { navigate(`trade/${id}`); }
function indicatorControls() {
  const items = [['vwap', 'VWAP'], ['sma9', 'SMA 9'], ['sma20', 'SMA 20'], ['ema9', 'EMA 9'], ['ema20', 'EMA 20'], ['bollinger', 'Bollinger'], ['volume', 'Volume']];
  return `<div class="indicator-controls">${items.map(([key, label]) => `<label><input type="checkbox" data-indicator="${key}" ${indicatorState[key] ? 'checked' : ''}> ${label}</label>`).join('')}</div>`;
}
async function detail() {
  const [data, tagResponse] = await Promise.all([api(`/api/v1/trades/${selected}`), api('/api/v1/tags')]);
  const t = data.trade, tags = tagResponse || [], tradeTags = t.tags || [], executions = data.executions || [];
  setTimeout(() => loadChart(t.id), 0);
  const m = data.excursion_metrics || {}, ratio = value => value == null ? 'N/A' : `${(value * 100).toFixed(1)}%`;
  return `<button onclick="view='trades';render()">← Trades</button><h2>${esc(t.symbol)} · ${t.direction} · <span class="${t.net >= 0 ? 'green' : 'red'}">${money(t.net)}</span></h2><section class="panel"><div class="toolbar"><button onclick="loadChart(${t.id}, '1m')">1 minute</button><button onclick="loadChart(${t.id}, '5m')">5 minute</button><button onclick="loadChart(${t.id}, '1m', 'full')">Full session</button><button onclick="enrich(${t.id})">Calculate MFE / MAE</button></div>${indicatorControls()}<div id="chart" class="chart"></div><p id="chart-status" class="muted">Loading chart…</p><small>TradingView Lightweight Charts™</small></section><div class="detail"><section class="panel"><h3>Executions</h3><table><tbody>${executions.map(e => `<tr><td>${dt(e.at)}</td><td>${e.action.toUpperCase()} ${e.quantity}</td><td>${money(e.price)}</td></tr>`).join('')}</tbody></table><p>Gross ${money(t.gross)} · Costs ${money(t.commissions + t.fees)} · Net ${money(t.net)}</p></section><section class="panel"><h3>Journal</h3><textarea id="note" placeholder="Private trade note">${esc(t.note)}</textarea><h4>Tags</h4><div class="tag-list">${tags.filter(tag => !tag.archived || tradeTags.some(current => current.id === tag.id)).map(tag => `<label class="tag"><input type="checkbox" value="${tag.id}" ${tradeTags.some(current => current.id === tag.id) ? 'checked' : ''}> <span style="background:${esc(tag.color)}"></span>${esc(tag.name)}</label>`).join('') || '<span class="muted">No tags yet.</span>'}</div><div class="toolbar"><input id="new-tag" placeholder="New tag name"><input id="new-tag-color" type="color" value="#58a6ff"><button onclick="createTag()">Create tag</button><button onclick="saveTrade(${t.id})">Save journal</button></div><p class="muted">MFE: ${data.excursion?.mfe == null ? 'Not calculated' : money(data.excursion.mfe)} · MAE: ${data.excursion?.mae == null ? 'Not calculated' : money(data.excursion.mae)}<br>MFE/share: ${m.mfe_per_share == null ? 'N/A' : money(m.mfe_per_share * 1e6)} · MAE/share: ${m.mae_per_share == null ? 'N/A' : money(m.mae_per_share * 1e6)} · Capture: ${ratio(m.capture_ratio)}</p><p class="muted">${esc(data.excursion?.source || '')} ${esc(data.excursion?.completeness || '')}<br>${esc(data.excursion?.warnings || data.massive_status)}</p></section></div>`;
}
function bindIndicatorControls(id, timeframe, mode) {
  document.querySelectorAll('[data-indicator]').forEach(input => input.onchange = () => { indicatorState[input.dataset.indicator] = input.checked; localStorage.setItem('tale-indicators', JSON.stringify(indicatorState)); loadChart(id, timeframe, mode); });
}
async function loadChart(id, timeframe = '1m', mode = 'focus') {
  const data = await api(`/api/v1/trades/${id}/chart?timeframe=${timeframe}${mode === 'full' ? '&view=full_session' : ''}`), el = $('#chart'), status = $('#chart-status');
  if (!el || !status) return;
  status.textContent = data.status + (data.source ? ` · ${data.source}` : ''); el.innerHTML = '';
  bindIndicatorControls(id, timeframe, mode);
  if (!data.bars?.length) return;
  const chart = LightweightCharts.createChart(el, { height: 340, layout: { background: { color: '#161b22' }, textColor: '#c9d1d9' }, grid: { vertLines: { color: '#21262d' }, horzLines: { color: '#21262d' } } });
  const candles = chart.addSeries(LightweightCharts.CandlestickSeries);
  candles.setData(data.bars.map(bar => ({ time: Math.floor(bar.time / 1000), open: bar.open, high: bar.high, low: bar.low, close: bar.close })));
  const markers = (data.executions || []).map(e => ({ time: Math.floor(new Date(e.at).getTime() / 1000), position: e.action === 'buy' ? 'belowBar' : 'aboveBar', color: e.action === 'buy' ? '#3fb950' : '#f85149', shape: e.action === 'buy' ? 'arrowUp' : 'arrowDown', text: `${e.action.toUpperCase()} ${e.quantity} @ ${(e.price / 1e6).toFixed(2)}` }));
  if (data.excursion?.mfe_at > 0) markers.push({ time: Math.floor(data.excursion.mfe_at / 1e6), position: 'aboveBar', color: '#f0b429', shape: 'circle', text: `MFE ${(data.excursion.mfe / 1e6).toFixed(2)}` });
  if (data.excursion?.mae_at > 0) markers.push({ time: Math.floor(data.excursion.mae_at / 1e6), position: 'belowBar', color: '#ff7b72', shape: 'circle', text: `MAE ${(data.excursion.mae / 1e6).toFixed(2)}` });
  LightweightCharts.createSeriesMarkers(candles, markers);
  const series = [['vwap', '#f0b429', 'vwap'], ['sma9', '#58a6ff', 'sma9'], ['sma20', '#bc8cff', 'sma20'], ['ema9', '#3fb950', 'ema9'], ['ema20', '#f85149', 'ema20'], ['upper', '#888', 'bollinger'], ['middle', '#666', 'bollinger'], ['lower', '#888', 'bollinger']];
  for (const [name, color, toggle] of series) if (indicatorState[toggle] && data.indicators?.[name]?.length) { const line = chart.addSeries(LightweightCharts.LineSeries, { color, lineWidth: 1, lastValueVisible: false }); line.setData(data.indicators[name].map(point => ({ time: Math.floor(point.time / 1000), value: point.value }))); }
  if (data.average_entry?.length) { const basis = chart.addSeries(LightweightCharts.LineSeries, { color: '#ffffff', lineWidth: 2, lineStyle: LightweightCharts.LineStyle.Dashed, lastValueVisible: false }); basis.setData(data.average_entry.map(point => ({ time: Math.floor(point.time / 1000), value: point.value }))); }
  if (indicatorState.volume) { const volume = chart.addSeries(LightweightCharts.HistogramSeries, { priceFormat: { type: 'volume' }, priceScaleId: '' }); volume.setData(data.bars.map(bar => ({ time: Math.floor(bar.time / 1000), value: bar.volume, color: '#388bfd88' }))); }
  chart.timeScale().fitContent();
}
async function saveTrade(id) { const tagIds = [...document.querySelectorAll('.tag-list input:checked')].map(input => Number(input.value)); await api(`/api/v1/trades/${id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ note: $('#note').value, tag_ids: tagIds }) }); alert('Journal saved'); render(); }
async function createTag() { const name = $('#new-tag').value.trim(); if (!name) return; await api('/api/v1/tags', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name, color: $('#new-tag-color').value }) }); render(); }
async function enrich(id) { try { const result = await api(`/api/v1/trades/${id}/enrich`, { method: 'POST' }); alert(`MFE ${money(result.mfe)} · MAE ${money(result.mae)} (${result.source}, ${result.completeness})`); render(); } catch (error) { alert(error.message); } }

async function calendar() {
  const year = calendarMonth.getFullYear(), month = calendarMonth.getMonth(), first = new Date(year, month, 1), last = new Date(year, month + 1, 0);
  const lead = first.getDay(), gridStart = new Date(year, month, 1 - lead), trail = 6 - last.getDay(), gridEnd = new Date(year, month + 1, trail);
  const days = await api(`/api/v1/calendar?start=${localDate(gridStart)}&end=${localDate(gridEnd)}`);
  let total = 0, green = 0, red = 0, scratch = 0;
  for (let day = 1; day <= last.getDate(); day++) {
    const item = days[localDate(new Date(year, month, day))], net = item?.net || 0;
    if (item) { total += net; if (net > 10000) green++; else if (net < -10000) red++; else scratch++; }
  }
  let html = `<style>.day-grid{grid-template-columns:repeat(7,minmax(0,1fr)) minmax(135px,.8fr)}.week-total{min-height:82px;padding:10px;border:1px solid #30363d;border-radius:5px;background:#161b22;display:flex;flex-direction:column;justify-content:center}.week-total strong{font-size:17px}@media(max-width:850px){.day-grid{overflow-x:auto;grid-template-columns:repeat(7,minmax(105px,1fr)) minmax(135px,.8fr)}}</style><div class="toolbar"><button onclick="changeMonth(-1)">←</button><button onclick="calendarMonth=new Date();render()">Today</button><h2>${first.toLocaleString(undefined, { month: 'long', year: 'numeric' })}</h2><button onclick="changeMonth(1)">→</button></div><div class="day-grid">${['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Week P&L'].map(day => `<b>${day}</b>`).join('')}`;
  for (let date = new Date(gridStart); date <= gridEnd; date.setDate(date.getDate() + 1)) {
    const key = localDate(date), inMonth = date.getMonth() === month, item = days[key], net = item?.net || 0;
    html += inMonth ? `<div class="day clickable ${net > 10000 ? 'positive' : net < -10000 ? 'negative' : 'scratch'}" onclick="showDay('${key}')"><b>${date.getDate()}</b>${item ? `<br>${money(net)}<br><small>${item.trades} trades</small>` : ''}</div>` : '<div></div>';
    if (date.getDay() === 6) {
      const weekStart = new Date(date); weekStart.setDate(date.getDate() - 6);
      let weekNet = 0, weekTrades = 0;
      for (let offset = 0; offset < 7; offset++) {
        const weekDate = new Date(weekStart); weekDate.setDate(weekStart.getDate() + offset);
        const weekItem = days[localDate(weekDate)];
        if (weekItem) { weekNet += weekItem.net; weekTrades += weekItem.trades; }
      }
      html += `<div class="week-total"><span class="muted">${weekStart.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}–${date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</span><strong class="${weekNet >= 0 ? 'green' : 'red'}">${money(weekNet)}</strong><small>${weekTrades} trades</small></div>`;
    }
  }
  return `<div class="cards">${metric(`${first.toLocaleString(undefined, { month: 'long' })} P&L`, money(total), total >= 0 ? 'green' : 'red')}${metric('Trading days', green + red + scratch)}${metric('Green / red / scratch', `${green} / ${red} / ${scratch}`)}</div>${html}</div><p class="muted">Weekly P&L uses complete Sunday–Saturday calendar weeks; boundary weeks can include adjacent-month trading days.</p>`;
}
function changeMonth(amount) { calendarMonth = new Date(calendarMonth.getFullYear(), calendarMonth.getMonth() + amount, 1); render(); }
function showDay(day) { navigate(`day/${day}`); }
async function dayDetail() {
  const range = `start=${selectedDay}&end=${selectedDay}`;
  const [rows, journal, summary, equity, drawdown] = await Promise.all([
    api(`/api/v1/trades?${range}`), api(`/api/v1/day-notes/${selectedDay}`),
    api(`/api/v1/analytics/summary?${range}`), api(`/api/v1/analytics/equity?${range}`),
    api(`/api/v1/analytics/equity?${range}&series=drawdown`)
  ]);
  setTimeout(() => drawEquity(equity, drawdown, '#day-equity-chart'), 0);
  const excursionRatio = summary.average_mae && summary.average_mfe != null ? summary.average_mfe / Math.abs(summary.average_mae) : null;
  const totalCosts = summary.commissions + summary.fees;
  const titleDate = new Date(`${selectedDay}T12:00:00`).toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric' });
  return `<div class="toolbar"><button onclick="navigate('calendar')">← Calendar</button><button onclick="shiftDay(-1)">← Previous day</button><button onclick="shiftDay(1)">Next day →</button><h2>${titleDate}</h2></div>
    <section class="panel"><div class="toolbar"><h3 class="${summary.net_pnl >= 0 ? 'green' : 'red'}">Day P&amp;L ${money(summary.net_pnl * 1e6)}</h3><button onclick="enrichSelectedDay()">Calculate day MFE / MAE</button></div>
      <div class="detail"><div><h3>Intraday realized equity</h3><div id="day-equity-chart" class="small-chart"></div></div>
      <div class="cards">${[
        metric('Total trades', summary.total_trades), metric('Win rate', percent(summary.win_rate)),
        metric('Total volume', Number(summary.total_volume || 0).toLocaleString()), metric('Commissions / fees', money(totalCosts * 1e6)),
        metric('Net P&L', money(summary.net_pnl * 1e6), summary.net_pnl >= 0 ? 'green' : 'red'),
        metric('Avg trade', money(summary.average_trade * 1e6)), metric('Profit factor', summary.profit_factor ? decimal(summary.profit_factor) : 'N/A'),
        metric('MFE / MAE ratio', excursionRatio == null ? 'N/A' : decimal(excursionRatio)),
        metric('Average MFE', summary.average_mfe == null ? 'N/A' : money(summary.average_mfe * 1e6), 'green'),
        metric('Average MAE', summary.average_mae == null ? 'N/A' : money(summary.average_mae * 1e6), 'red'),
        metric('Largest gain', money(summary.largest_winner * 1e6), 'green'), metric('Largest loss', money(summary.largest_loser * 1e6), 'red')
      ].join('')}</div></div>
    </section>
    <section class="panel"><div class="toolbar"><h3>Day journal</h3><button onclick="insertDayTemplate()">Insert review template</button><button onclick="saveDayNote()">Save day note</button></div><textarea id="day-note" placeholder="What worked, what did not, and what will you repeat tomorrow?">${esc(journal.note)}</textarea></section>
    <section class="panel"><h3>${rows.length} completed trades</h3><table><thead><tr><th>Exit time</th><th>Symbol</th><th>Side</th><th>Volume</th><th>Execs</th><th>Hold</th><th>Net P&L</th><th>MFE</th><th>MAE</th><th>Tags</th><th>Review</th></tr></thead><tbody>${rows.map(row => `<tr><td>${new Date(row.exit_at / 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}</td><td><strong>${esc(row.symbol)}</strong></td><td>${esc(row.direction)}</td><td>${Number(row.entered || 0).toLocaleString()}</td><td>${row.execution_count}</td><td>${duration((row.exit_at - row.entry_at) / 6e7)}</td><td class="${row.net >= 0 ? 'green' : 'red'}">${money(row.net)}</td><td>${row.excursion ? money(row.excursion.mfe) : '—'}</td><td>${row.excursion ? money(row.excursion.mae) : '—'}</td><td>${(row.tags || []).map(tag => esc(tag.name)).join(', ') || '—'}</td><td><button onclick="showTrade(${row.id})">Chart &amp; journal</button></td></tr>`).join('')}</tbody></table>${rows.length ? '' : '<p class="muted">No completed trades on this date.</p>'}</section>`;
}
async function saveDayNote() { await api(`/api/v1/day-notes/${selectedDay}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ note: $('#day-note').value }) }); alert('Day note saved'); }
function shiftDay(amount) { const day = new Date(`${selectedDay}T12:00:00`); day.setDate(day.getDate() + amount); navigate(`day/${localDate(day)}`); }
function insertDayTemplate() {
  const template = `Market context:\n\nWhat I did well:\n\nMistakes / rule breaks:\n\nBest setup and why:\n\nRisk and execution review:\n\nOne improvement for the next session:\n`;
  if (!$('#day-note').value.trim() || confirm('Replace the current day note with the review template?')) $('#day-note').value = template;
}
async function enrichSelectedDay() {
  const result = await api(`/api/v1/enrichment/range?start=${selectedDay}&end=${selectedDay}`, { method: 'POST' });
  alert(`MFE / MAE calculated for ${result.completed} of ${result.requested} trades.`);
  render();
}

async function importing() { const batches = await api('/api/v1/imports'); return `<h2>Import Thinkorswim executions</h2><div class="drop"><input id="file" type="file" accept=".csv,.zip"><p>Choose a Thinkorswim CSV or ZIP archive. Preview is local and requires explicit commit.</p><button onclick="previewImport()">Preview</button></div><div id="preview"></div><section class="panel"><h3>Previous imports</h3><table>${batches.map(batch => `<tr><td>${esc(batch.filename)}</td><td>${batch.accepted_rows} accepted</td><td>${batch.rejected_rows} rejected</td></tr>`).join('')}</table></section>`; }
let token = '';
async function previewImport() { const file = $('#file').files[0]; if (!file) return; const form = new FormData(); form.append('file', file); const preview = await api('/api/v1/imports/preview', { method: 'POST', body: form }); token = preview.token; $('#preview').innerHTML = `<section class="panel"><h3>Preview: ${preview.accepted_rows} accepted, ${preview.skipped_rows} skipped</h3><p>${preview.files} file(s) · ${esc(preview.account)} · ${preview.symbols.map(esc).join(', ')}</p><p>${preview.rejected_rows.length} rejected rows.</p><details><summary>Warnings and rejected rows</summary><ul>${[...preview.warnings, ...preview.rejected_rows.map(row => row.reason)].map(esc).map(item => `<li>${item}</li>`).join('') || '<li>None</li>'}</ul></details><button onclick="commitImport()">Commit import</button></section>`; }
async function commitImport() { const file = $('#file').files[0]; const result = await api('/api/v1/imports/commit', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token, filename: file.name }) }); alert(`${result.message} New executions: ${result.new_executions}`); navigate('trades'); }

async function settings() { const settings = await api('/api/v1/settings'), tags = await api('/api/v1/tags'); const timezone = settings.app.Timezone || settings.app.timezone, tolerance = settings.import.ScratchTolerance ?? settings.import.scratch_tolerance, timeframe = settings.chart.DefaultTimeframe || settings.chart.default_timeframe; return `<h2>Settings & privacy</h2><section class="panel"><div class="toolbar"><label>Timezone <input id="setting-timezone" value="${esc(timezone)}"></label><label>Scratch tolerance <input id="setting-tolerance" type="number" min="0" step="0.01" value="${tolerance}"></label><label>Default chart <select id="setting-timeframe"><option value="1m" ${timeframe === '1m' ? 'selected' : ''}>1 minute</option><option value="5m" ${timeframe === '5m' ? 'selected' : ''}>5 minute</option></select></label><button onclick="saveSettings()">Save settings</button></div><p>Database: ${esc(settings.storage.path)} · Massive: ${settings.massive_configured ? 'configured' : 'not configured'}. The key never reaches this browser.</p><button onclick="backup()">Create local backup</button><h3>Tags</h3><div class="tag-list">${tags.map(tag => `<span class="tag"><span style="background:${esc(tag.color)}"></span>${esc(tag.name)}${tag.archived ? ' (archived)' : ''}</span>`).join('') || '<span class="muted">Create tags from a trade journal.</span>'}</div><h3 id="privacy">Privacy</h3><p>Thinkorswim imports, notes, tags and analytics remain in your local SQLite database. Only a symbol and historical interval are sent to Massive for requested market data. Nothing is sent to TradeTally or telemetry services.</p></section>`; }
async function saveSettings() { await api('/api/v1/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ timezone: $('#setting-timezone').value.trim(), scratch_tolerance: Number($('#setting-tolerance').value), default_timeframe: $('#setting-timeframe').value }) }); alert('Settings saved locally.'); render(); }
async function backup() { const result = await api('/api/v1/backup', { method: 'POST' }); alert(`Backup created: ${result.path}`); }
async function render() {
  try {
    $('#app').innerHTML = view === 'dashboard' ? await dashboard() : view === 'calendar' ? await calendar() : view === 'trades' ? await trades() : view === 'import' ? await importing() : view === 'detail' ? await detail() : view === 'day' ? await dayDetail() : await settings();
    if (view === 'detail') {
      const back = document.querySelector('#app > button');
      if (back) { back.removeAttribute('onclick'); back.onclick = () => history.length > 1 ? history.back() : navigate('trades'); back.textContent = '← Previous view'; }
    }
  } catch (error) {
    $('#app').innerHTML = `<section class="panel red">${esc(error.message)}</section>`;
  }
}
document.querySelectorAll('nav button').forEach(button => button.onclick = () => navigate(button.dataset.view));
try { const saved = JSON.parse(localStorage.getItem('tale-date-range') || '{}'); dateStart = saved.start || ''; dateEnd = saved.end || ''; datePreset = saved.preset || ''; } catch (_) {}
window.addEventListener('hashchange', () => { applyRoute(); render(); });
applyRoute();
render();
