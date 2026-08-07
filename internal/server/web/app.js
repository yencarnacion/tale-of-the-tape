const $ = selector => document.querySelector(selector);
const money = value => new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format((value || 0) / 1e6);
const dt = value => new Date(typeof value === 'string' ? value : value / 1000).toLocaleString();
const esc = value => String(value ?? '').replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
let view = 'dashboard', selected = null, selectedDay = '', tradePage = 0, tradeSort = 'exit_at', tradeDescending = true;
let dateStart = '', dateEnd = '', datePreset = '', calendarMonth = new Date();
let cohortSymbol = '', cohortDirection = '', cohortOutcome = '', cohortHolding = '';
let executionChartMode = 'both';
let candleChartPreset = localStorage.getItem('tale-candle-preset') || 'executions', activeChartTimeframe = '1m', activeChartMode = 'focus';
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
  if (cohortSymbol) parts.push(`symbol=${encodeURIComponent(cohortSymbol)}`);
  if (cohortDirection) parts.push(`direction=${encodeURIComponent(cohortDirection)}`);
  if (cohortOutcome) parts.push(`outcome=${encodeURIComponent(cohortOutcome)}`);
  if (cohortHolding) parts.push(`holding_time=${encodeURIComponent(cohortHolding)}`);
  if (extra) parts.push(extra);
  return parts.length ? `?${parts.join('&')}` : '';
}
function filters() {
  const label = dateStart && dateEnd ? `Showing ${datePreset === 'week' ? 'this week' : datePreset === 'month' ? 'month to date' : datePreset === 'year' ? 'year to date' : 'custom range'}: ${dateStart} through ${dateEnd}` : 'Showing all imported dates';
  const holdLabels = { under_5m: 'under 5m', '5_30m': '5–30m', '30_60m': '30–60m', '60m_plus': '60m+' };
  const cohort = [cohortSymbol && cohortSymbol.toUpperCase(), cohortDirection, cohortOutcome, holdLabels[cohortHolding]].filter(Boolean).join(' · ');
  return `<section class="range-filter"><div class="toolbar"><button class="${datePreset === 'week' ? 'active' : ''}" onclick="setDatePreset('week')">This week</button><button class="${datePreset === 'month' ? 'active' : ''}" onclick="setDatePreset('month')">Month to date</button><button class="${datePreset === 'year' ? 'active' : ''}" onclick="setDatePreset('year')">Year to date</button><label>Start <input id="start" type="date" value="${dateStart}"></label><label>End <input id="end" type="date" value="${dateEnd}"></label><button onclick="applyDates()">Apply</button><button class="${!dateStart && !dateEnd ? 'active' : ''}" onclick="clearDates()">All dates</button>${dateStart && dateEnd ? '<button onclick="enrichRange()">Calculate range MFE / MAE</button>' : ''}</div>
    <details class="cohort-filter" ${cohort ? 'open' : ''}><summary>Trade cohort filters${cohort ? ` · ${esc(cohort)}` : ''}</summary><div class="toolbar">
      <label>Symbol <input id="cohort-symbol" value="${esc(cohortSymbol)}" placeholder="e.g. NVDA"></label>
      <label>Side <select id="cohort-direction"><option value="">All</option><option value="long" ${cohortDirection === 'long' ? 'selected' : ''}>Long</option><option value="short" ${cohortDirection === 'short' ? 'selected' : ''}>Short</option></select></label>
      <label>Outcome <select id="cohort-outcome"><option value="">All</option><option value="win" ${cohortOutcome === 'win' ? 'selected' : ''}>Winners</option><option value="loss" ${cohortOutcome === 'loss' ? 'selected' : ''}>Losers</option><option value="scratch" ${cohortOutcome === 'scratch' ? 'selected' : ''}>Scratches</option></select></label>
      <label>Hold <select id="cohort-holding"><option value="">All</option><option value="under_5m" ${cohortHolding === 'under_5m' ? 'selected' : ''}>Under 5m</option><option value="5_30m" ${cohortHolding === '5_30m' ? 'selected' : ''}>5–30m</option><option value="30_60m" ${cohortHolding === '30_60m' ? 'selected' : ''}>30–60m</option><option value="60m_plus" ${cohortHolding === '60m_plus' ? 'selected' : ''}>60m+</option></select></label>
      <button onclick="applyCohort()">Analyze cohort</button><button onclick="clearCohort()">Clear cohort</button>
    </div></details><p><strong>${label}</strong>${cohort ? ` · Cohort: ${esc(cohort)}` : ''}</p></section>`;
}
const localDate = date => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
const clockTime = time => new Date(Number(time) * 1000).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
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
function applyCohort() {
  cohortSymbol = $('#cohort-symbol').value.trim(); cohortDirection = $('#cohort-direction').value;
  cohortOutcome = $('#cohort-outcome').value; cohortHolding = $('#cohort-holding').value;
  localStorage.setItem('tale-cohort', JSON.stringify({ symbol: cohortSymbol, direction: cohortDirection, outcome: cohortOutcome, holding: cohortHolding }));
  tradePage = 0; render();
}
function clearCohort() {
  cohortSymbol = ''; cohortDirection = ''; cohortOutcome = ''; cohortHolding = '';
  localStorage.removeItem('tale-cohort'); tradePage = 0; render();
}
function dateIsInRange(date) { return (!dateStart || date >= dateStart) && (!dateEnd || date <= dateEnd); }
function calendarRange(start, end) {
  const from = dateStart && dateStart > start ? dateStart : start;
  const to = dateEnd && dateEnd < end ? dateEnd : end;
  return from > to ? '' : `start=${encodeURIComponent(from)}&end=${encodeURIComponent(to)}`;
}
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
function orderedBreakdown(items, order = []) {
  const rank = new Map(order.map((name, index) => [name, index]));
  return [...(items || [])].sort((a, b) => (rank.get(a.name) ?? 999) - (rank.get(b.name) ?? 999) || a.name.localeCompare(b.name));
}
function edgeBars(items, title, note, order = []) {
  items = orderedBreakdown(items, order);
  if (!items.length) return '';
  const max = Math.max(...items.map(item => Math.abs(item.summary.net_pnl)), 1);
  return `<section class="panel edge-panel"><h3>${title}</h3><p class="muted">${note}</p><div class="edge-bars">${items.map(item => {
    const net = item.summary.net_pnl, width = Math.max(2, Math.abs(net) / max * 100);
    return `<div class="edge-row"><div class="edge-label"><strong>${esc(item.name)}</strong><span>${item.summary.total_trades} trades · ${percent(item.summary.win_rate)}</span></div><div class="edge-track"><span class="${net >= 0 ? 'edge-positive' : 'edge-negative'}" style="width:${width}%"></span></div><strong class="${net >= 0 ? 'green' : 'red'}">${money(net * 1e6)}</strong></div>`;
  }).join('')}</div></section>`;
}
function dayPerformance(calendar) {
  const days = Object.values(calendar || {}).sort((a, b) => a.date.localeCompare(b.date));
  const values = days.map(day => ({ ...day, dollars: day.net / 1e6 }));
  const green = values.filter(day => day.dollars > 0), red = values.filter(day => day.dollars < 0);
  const flat = values.length - green.length - red.length;
  const best = values.length ? values.reduce((a, b) => b.dollars > a.dollars ? b : a) : null;
  const worst = values.length ? values.reduce((a, b) => b.dollars < a.dollars ? b : a) : null;
  const average = items => items.length ? items.reduce((sum, day) => sum + day.dollars, 0) / items.length : null;
  const months = new Map();
  values.forEach(day => months.set(day.date.slice(0, 7), (months.get(day.date.slice(0, 7)) || 0) + day.dollars));
  const monthly = [...months].map(([month, dollars]) => ({ month, dollars }));
  const bestMonth = monthly.length ? monthly.reduce((a, b) => b.dollars > a.dollars ? b : a) : null;
  const worstMonth = monthly.length ? monthly.reduce((a, b) => b.dollars < a.dollars ? b : a) : null;
  return { values, green, red, flat, best, worst, averageGreen: average(green), averageRed: average(red), bestMonth, worstMonth, averageMonth: monthly.length ? monthly.reduce((sum, month) => sum + month.dollars, 0) / monthly.length : null };
}
function datedMoney(item, key = 'date') {
  if (!item) return 'N/A';
  const label = key === 'month'
    ? new Date(`${item.month}-01T12:00:00`).toLocaleDateString(undefined, { month: 'short', year: 'numeric' })
    : new Date(`${item.date}T12:00:00`).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  return `${money(item.dollars * 1e6)} <small>${label}</small>`;
}

async function dashboard() {
  const [summary, equity, breakdown, drawdown, calendar, risk] = await Promise.all([
    api('/api/v1/analytics/summary' + query()), api('/api/v1/analytics/equity' + query()), api('/api/v1/analytics/breakdowns' + query()), api('/api/v1/analytics/equity' + query('series=drawdown')), api('/api/v1/calendar' + query()), api('/api/v1/analytics/risk' + query())
  ]);
  const days = dayPerformance(calendar);
  setTimeout(() => { drawEquity(equity, drawdown); drawDailyPnL(days.values); drawRollingRisk(risk); }, 0);
  const broker = summary.broker_ytd == null || (datePreset && datePreset !== 'year') ? '' : `<section class="panel"><h3>Broker year-to-date reconciliation</h3><p><strong class="${summary.broker_ytd >= 0 ? 'green' : 'red'}">${money(summary.broker_ytd * 1e6)}</strong> through ${esc(summary.broker_ytd_date)} · Thinkorswim statement P/L YTD</p><p class="muted">Closed intraday journal P&L below is reconstructed from imported executions. Broker YTD includes carried-position cost basis that execution-only exports cannot reproduce exactly. Broker-reported commissions and fees YTD: ${money(summary.broker_fees_ytd * 1e6)}.</p></section>`;
  const kelly = (value, preliminary) => value == null ? 'Undefined' : `${percent(value)}${preliminary ? ` prelim · ${summary.kelly_sample}/${summary.kelly_minimum_sample}` : ''}`;
  const preliminaryKelly = summary.raw_kelly == null;
  const detailed = [
    ['Total Gain/Loss', money(summary.net_pnl * 1e6)], ['Largest Gain', money(summary.largest_winner * 1e6)], ['Largest Loss', money(summary.largest_loser * 1e6)],
    ['Average Daily Gain/Loss', money(summary.average_daily_pnl * 1e6)], ['Average Daily Volume', Math.round(summary.average_daily_volume || 0).toLocaleString()], ['Average Per-share Gain/Loss', summary.average_per_share == null ? 'N/A' : money(summary.average_per_share * 1e6)],
    ['Average Trade Gain/Loss', money(summary.average_trade * 1e6)], ['Average Winning Trade', money(summary.average_winner * 1e6)], ['Average Losing Trade', money(summary.average_loser * 1e6)],
    ['Total Number of Trades', summary.total_trades], ['Number of Winning Trades', countRate(summary.wins, summary.total_trades)], ['Number of Losing Trades', countRate(summary.losses, summary.total_trades)],
    ['Total Trading Days', days.values.length], ['Winning Days', countRate(days.green.length, days.values.length)], ['Losing Days', countRate(days.red.length, days.values.length)],
    ['Breakeven Days', countRate(days.flat, days.values.length)], ['Average Winning Day', days.averageGreen == null ? 'N/A' : money(days.averageGreen * 1e6)], ['Average Losing Day', days.averageRed == null ? 'N/A' : money(days.averageRed * 1e6)],
    ['Best Day', datedMoney(days.best)], ['Worst Day', datedMoney(days.worst)], ['Max Consecutive Green Days', summary.max_green_day_streak],
    ['Max Consecutive Red Days', summary.max_red_day_streak], ['Best Month', datedMoney(days.bestMonth, 'month')], ['Worst Month', datedMoney(days.worstMonth, 'month')],
    ['Average Monthly P&L', days.averageMonth == null ? 'N/A' : money(days.averageMonth * 1e6)],
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
  ].join('')}</div><section class="chart-grid"><div class="panel"><h2>Cumulative P&L & drawdown</h2><div id="equity-chart" class="small-chart"></div><p class="muted">Shows whether your edge is compounding and how deep the current equity decline is.</p></div><div class="panel"><h2>Net P&L by trading day</h2><div id="daily-pnl-chart" class="small-chart"></div><p class="muted">Use the distribution of green and red sessions to spot outsized loss days and inconsistent risk.</p></div></section>
    <h2 class="section-title">Where your edge works</h2><section class="edge-grid">
      ${edgeBars(breakdown.entry_time, 'Performance by entry time', 'Net P&L for trades grouped by entry half-hour. Look for time windows to stop trading or size down.')}
      ${edgeBars(breakdown.weekday, 'Performance by weekday', 'Separates a recurring weekday effect from one unusually good or bad session.', ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'])}
      ${edgeBars(breakdown.holding_time, 'Performance by hold time', 'Shows whether quick decisions or extended holds produce the stronger realized edge.', ['under 5m', '5–30m', '30–60m', '60m+'])}
    </section>
    <h2 class="section-title">Risk regime</h2><div class="cards risk-cards">
      ${metric('Current drawdown', money(risk.current_drawdown * 1e6), risk.current_drawdown ? 'red' : 'green')}
      ${metric('Current drawdown length', `${risk.current_drawdown_trades} trades · ${decimal(risk.current_drawdown_days, 1)}d`)}
      ${metric('Average drawdown', money(risk.average_drawdown * 1e6), 'red')}
      ${metric('Worst drawdown', money(risk.biggest_drawdown * 1e6), 'red')}
      ${metric('Avg recovery length', `${decimal(risk.average_drawdown_trades, 1)} trades · ${decimal(risk.average_drawdown_days, 1)}d`)}
      ${metric('Drawdown episodes', risk.episodes)}
    </div><section class="panel"><h3>Rolling 20-trade edge and volatility</h3><div id="rolling-risk-chart" class="small-chart"></div><p class="muted">Green is average net P&L per trade; amber is trade P&L standard deviation. Falling expectancy with rising volatility is a practical size-down signal—not a prediction.</p></section>
    <section class="panel"><h2>Detailed statistics</h2><table><tbody>${detailed.map(([name, value]) => `<tr><th>${name}</th><td>${value}</td></tr>`).join('')}</tbody></table><p class="muted">Day and month statistics use realized net P&L on each trade's exit date. SQN requires at least 30 trades. Probability of random chance is the exact two-sided binomial probability for the observed win/loss split. Volume counts shares opened per completed round trip.</p></section>${patterns(breakdown.direction, 'Long vs short performance')}${patterns(breakdown.tag, 'Performance by tag')}${patterns(breakdown.symbol, 'Performance by symbol')}<section class="panel"><h2>Review, not execution</h2><p class="muted">Metrics use net P&L after commissions and fees. Treat small cohorts as hypotheses, not trading rules; compare both trade count and win rate before changing your plan.</p></section>`;
}
function drawEquity(points, drawdown, selector = '#equity-chart', intraday = false) {
  const el = $(selector);
  if (!el || !points?.length) { if (el) el.textContent = 'No closed trades in this range.'; return; }
  const options = { height: 220, layout: { background: { color: '#161b22' }, textColor: '#c9d1d9' }, grid: { vertLines: { color: '#21262d' }, horzLines: { color: '#21262d' } } };
  if (intraday) {
    options.localization = { timeFormatter: clockTime };
    options.timeScale = { timeVisible: true, secondsVisible: false, tickMarkFormatter: clockTime };
  }
  const chart = LightweightCharts.createChart(el, options);
  const line = chart.addSeries(LightweightCharts.LineSeries, { color: '#58a6ff', lineWidth: 2 });
  line.setData(points); if (drawdown?.length) { const risk = chart.addSeries(LightweightCharts.LineSeries, { color: '#f85149', lineWidth: 1, lastValueVisible: false }); risk.setData(drawdown); } chart.timeScale().fitContent();
}
function drawDailyPnL(days) {
  const el = $('#daily-pnl-chart');
  if (!el || !days?.length) { if (el) el.textContent = 'No closed trades in this range.'; return; }
  const chart = LightweightCharts.createChart(el, { height: 220, layout: { background: { color: '#161b22' }, textColor: '#c9d1d9' }, grid: { vertLines: { color: '#21262d' }, horzLines: { color: '#21262d' } } });
  const bars = chart.addSeries(LightweightCharts.HistogramSeries, { base: 0, priceFormat: { type: 'price', precision: 2, minMove: 0.01 } });
  bars.setData(days.map(day => ({ time: day.date, value: day.dollars, color: day.dollars > 0 ? '#3fb950' : day.dollars < 0 ? '#f85149' : '#8b949e' })));
  chart.timeScale().fitContent();
}
function drawRollingRisk(risk) {
  const el = $('#rolling-risk-chart'), points = risk?.rolling || [];
  if (!el || !points.length) { if (el) el.textContent = `At least ${risk?.window || 20} trades are required.`; return; }
  const chart = LightweightCharts.createChart(el, { height: 220, layout: { background: { color: '#161b22' }, textColor: '#c9d1d9' }, grid: { vertLines: { color: '#21262d' }, horzLines: { color: '#21262d' } } });
  const edge = chart.addSeries(LightweightCharts.LineSeries, { color: '#3fb950', lineWidth: 2, title: 'Expectancy' });
  const volatility = chart.addSeries(LightweightCharts.LineSeries, { color: '#f0b429', lineWidth: 1, title: 'Volatility' });
  edge.setData(points.map(point => ({ time: point.time, value: point.expectancy })));
  volatility.setData(points.map(point => ({ time: point.time, value: point.volatility })));
  chart.timeScale().fitContent();
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
  const items = [['vwap', 'VWAP'], ['sma9', 'SMA 9'], ['sma20', 'SMA 20'], ['ema9', 'EMA 9'], ['ema20', 'EMA 20'], ['bollinger', 'Bollinger']];
  return `<details class="indicator-settings"><summary>Custom indicators</summary><div class="indicator-controls">${items.map(([key, label]) => `<label><input type="checkbox" data-indicator="${key}" ${indicatorState[key] ? 'checked' : ''}> ${label}</label>`).join('')}</div></details>`;
}
function candlePresetControls() {
  return `<div class="chart-mode candle-presets" role="group" aria-label="Candlestick chart detail"><button data-candle-preset="executions" class="${candleChartPreset === 'executions' ? 'active' : ''}" onclick="setCandleChartPreset('executions')">Executions</button><button data-candle-preset="context" class="${candleChartPreset === 'context' ? 'active' : ''}" onclick="setCandleChartPreset('context')">Trade context</button><button data-candle-preset="custom" class="${candleChartPreset === 'custom' ? 'active' : ''}" onclick="setCandleChartPreset('custom')">Custom</button></div>`;
}
function executionPath(executions, maxPosition = 0) {
  let position = 0, basis = 0, realized = 0, costs = 0, lastTime = 0;
  return executions.map((execution, index) => {
    const quantity = Number(execution.quantity || 0), price = Number(execution.price || 0);
    const signed = execution.action === 'buy' ? quantity : -quantity;
    const previousPosition = position;
    let change = !position ? 'OPEN' : Math.sign(position) === Math.sign(signed) ? 'ADD' : '';
    if (!position || Math.sign(position) === Math.sign(signed)) {
      basis = position ? (Math.abs(position) * basis + quantity * price) / (Math.abs(position) + quantity) : price;
      position += signed;
    } else {
      const closing = Math.min(Math.abs(position), quantity);
      realized += closing * (price - basis) * Math.sign(position);
      const next = position + signed;
      if (next && Math.sign(next) !== Math.sign(position)) basis = price;
      if (!next) basis = 0;
      position = next;
    }
    if (!change) change = !position ? 'EXIT' : Math.sign(position) === Math.sign(previousPosition) ? 'TRIM' : 'REVERSE';
    costs += Number(execution.commission || 0) + Number(execution.fees || 0);
    const rawTime = Math.floor(new Date(execution.at).getTime() / 1000);
    const time = Math.max(rawTime, lastTime + (index ? 1 : 0)); lastTime = time;
    const exposure = maxPosition ? Math.abs(position) / maxPosition : null;
    return { ...execution, position, previousPosition, change, exposure, basis, realized, costs, net: realized - costs, time };
  });
}
function polygonChartURL(base, ticker, date, execution, timezone) {
  if (!base || !execution) return '';
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: timezone || 'America/New_York', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(execution.at));
  const part = type => parts.find(item => item.type === type)?.value || '';
  const params = new URLSearchParams({ ticker, date, time: `${part('hour')}${part('minute')}`, resolution: '1m', signal: execution.action });
  return `${String(base).replace(/\/$/, '')}/api/open-chart?${params}`;
}
function polygonExecutionLinks(data, trade, executions) {
  if (!executions.length || !data.polygon_charts_url) return '';
  const entry = executions[0], exit = executions[executions.length - 1];
  const link = (label, execution) => `<a class="button polygon-link" href="${esc(polygonChartURL(data.polygon_charts_url, trade.symbol, data.trading_day, execution, data.timezone))}" target="_blank" rel="noopener noreferrer">${label} · ${esc(execution.action.toUpperCase())} ↗</a>`;
  return `<span class="toolbar-divider"></span>${link('Entry', entry)}${link('Exit', exit)}`;
}
async function detail() {
  const [data, tagResponse] = await Promise.all([api(`/api/v1/trades/${selected}`), api('/api/v1/tags')]);
  const t = data.trade, tags = tagResponse || [], tradeTags = t.tags || [], executions = data.executions || [];
  const dayEquity = await api(`/api/v1/analytics/equity?start=${encodeURIComponent(data.trading_day)}&end=${encodeURIComponent(data.trading_day)}`);
  const path = executionPath(executions, t.max_quantity);
  setTimeout(() => { loadChart(t.id); drawExecutionPath(path, dayEquity); }, 0);
  const m = data.excursion_metrics || {}, ratio = value => value == null ? 'N/A' : `${(value * 100).toFixed(1)}%`;
  return `<button onclick="view='trades';render()">← Trades</button><h2>${esc(t.symbol)} · ${t.direction} · <span class="${t.net >= 0 ? 'green' : 'red'}">${money(t.net)}</span></h2><section class="panel"><div class="toolbar"><button onclick="loadChart(${t.id}, '1m')">1 minute</button><button onclick="loadChart(${t.id}, '5m')">5 minute</button><button onclick="loadChart(${t.id}, '1m', 'full')">Full session</button><span class="toolbar-divider"></span>${candlePresetControls()}<button onclick="enrich(${t.id})">Calculate MFE / MAE</button>${polygonExecutionLinks(data, t, executions)}</div>${indicatorControls()}<div id="chart" class="chart"></div><p id="chart-status" class="muted">Loading chart…</p><p class="muted chart-help">Execution markers are numbered to match the ledger. Fills in the same candle are combined into one label. Entry and Exit open Polygon Charts in a new tab.</p><small>TradingView Lightweight Charts™</small></section>
    <section class="panel"><div class="toolbar"><h3>Trade and session P&amp;L path</h3><div class="chart-mode" role="group" aria-label="P&L chart view"><button class="${executionChartMode === 'trade' ? 'active' : ''}" onclick="setExecutionChartMode('trade')">Trade</button><button class="${executionChartMode === 'both' ? 'active' : ''}" onclick="setExecutionChartMode('both')">Trade + session</button><button class="${executionChartMode === 'session' ? 'active' : ''}" onclick="setExecutionChartMode('session')">Session</button></div><span class="chart-key">${executionChartMode !== 'session' ? '<i class="trade-key"></i>Selected trade' : ''}${executionChartMode === 'both' ? ' <i class="day-key"></i>Closed-trade day P&amp;L' : ''}${executionChartMode === 'session' ? '<i class="day-key"></i>Closed-trade day P&amp;L' : ''}</span></div><div id="execution-path-chart" class="small-chart"></div><p class="muted">The axis is fitted to actual P&amp;L values; execution labels do not change its range. Blue reconstructs realized net P&amp;L after each fill. Gray is the session’s cumulative P&amp;L as completed trades close, using ${esc(data.trading_day)} in the configured trading timezone.</p></section>
    <div class="detail"><section class="panel execution-ledger"><h3>Execution ledger</h3><table><thead><tr><th>#</th><th>Time</th><th>Decision</th><th>Fill</th><th>Price</th><th>Position after</th><th>Exposure</th><th>Realized net</th></tr></thead><tbody>${path.map((e, index) => `<tr><td><strong>${index + 1}</strong></td><td>${dt(e.at)}</td><td><span class="execution-type ${e.change.toLowerCase()}">${e.change}</span></td><td>${e.action.toUpperCase()} ${e.quantity}</td><td>${money(e.price)}</td><td>${Number(e.position).toLocaleString()}</td><td>${e.exposure == null ? '—' : percent(e.exposure)}</td><td class="${e.net >= 0 ? 'green' : 'red'}">${money(e.net)}</td></tr>`).join('')}</tbody></table><p>Gross ${money(t.gross)} · Costs ${money(t.commissions + t.fees)} · Net ${money(t.net)}</p></section><section class="panel"><div class="toolbar"><h3>Journal</h3><button onclick="insertTradeTemplate()">Insert review template</button></div><textarea id="note" placeholder="Private trade note">${esc(t.note)}</textarea><h4>Tags</h4><div class="tag-list current-tags">${tradeTags.map(tag => `<span class="tag"><span style="background:${esc(tag.color)}"></span>${esc(tag.name)}<button class="tag-remove" onclick="removeTradeTag(${t.id},${tag.id})" title="Remove ${esc(tag.name)}" aria-label="Remove ${esc(tag.name)}">×</button></span>`).join('') || '<span class="muted">No tags on this trade.</span>'}</div><div class="toolbar tag-entry"><input id="new-tag" list="tag-suggestions" autocomplete="off" placeholder="Add a tag" onkeydown="if(event.key==='Enter'){event.preventDefault();addTradeTag(${t.id})}"><datalist id="tag-suggestions">${tags.filter(tag => !tag.archived && !tradeTags.some(current => current.id === tag.id)).map(tag => `<option value="${esc(tag.name)}"></option>`).join('')}</datalist><input id="new-tag-color" type="color" value="#58a6ff" title="Color for a new tag"><button onclick="addTradeTag(${t.id})">Add tag</button><button onclick="saveTrade(${t.id})">Save journal</button></div><p class="muted">Start typing to reuse an existing tag. A new name creates and assigns the tag.</p><p class="muted">MFE: ${data.excursion?.mfe == null ? 'Not calculated' : money(data.excursion.mfe)} · MAE: ${data.excursion?.mae == null ? 'N/A' : money(data.excursion.mae)}<br>MFE/share: ${m.mfe_per_share == null ? 'N/A' : money(m.mfe_per_share * 1e6)} · MAE/share: ${m.mae_per_share == null ? 'N/A' : money(m.mae_per_share * 1e6)} · Capture: ${ratio(m.capture_ratio)}</p><p class="muted">${esc(data.excursion?.source || '')} ${esc(data.excursion?.completeness || '')}<br>${esc(data.excursion?.warnings || data.massive_status)}</p></section></div>`;
}
function drawExecutionPath(path, dayEquity) {
  const el = $('#execution-path-chart');
  if (!el || !path.length) { if (el) el.textContent = 'No executions available.'; return; }
  const tradeValues = path.map(point => point.net / 1e6);
  const dayValues = (dayEquity || []).map(point => point.value);
  const visibleValues = executionChartMode === 'trade' ? tradeValues : executionChartMode === 'session' ? dayValues : [...tradeValues, ...dayValues];
  const low = Math.min(0, ...visibleValues), high = Math.max(0, ...visibleValues);
  const span = Math.max(high - low, Math.abs(high), Math.abs(low), 1), padding = span * .12;
  const fixedRange = { minValue: low - padding, maxValue: high + padding };
  const fittedScale = { autoscaleInfoProvider: () => ({ priceRange: fixedRange }) };
  const chart = LightweightCharts.createChart(el, { height: 250, layout: { background: { color: '#161b22' }, textColor: '#c9d1d9' }, rightPriceScale: { scaleMargins: { top: .06, bottom: .06 } }, localization: { timeFormatter: clockTime }, timeScale: { timeVisible: true, secondsVisible: true, tickMarkFormatter: clockTime }, grid: { vertLines: { color: '#21262d' }, horzLines: { color: '#21262d' } } });
  if (executionChartMode !== 'trade' && dayEquity?.length) {
    const day = chart.addSeries(LightweightCharts.LineSeries, { ...fittedScale, color: '#8b949e', lineWidth: 2, lineStyle: LightweightCharts.LineStyle.Dashed, title: 'Day', lastValueVisible: true });
    day.setData(dayEquity);
  }
  if (executionChartMode === 'session') { chart.timeScale().fitContent(); return; }
  const line = chart.addSeries(LightweightCharts.LineSeries, { ...fittedScale, color: '#58a6ff', lineWidth: 2, priceLineVisible: true, title: 'Trade' });
  line.setData(path.map((point, index) => ({ time: point.time, value: tradeValues[index] })));
  const styles = {
    OPEN: { color: '#58a6ff', shape: 'circle', position: 'aboveBar' },
    ADD: { color: '#bc8cff', shape: 'arrowUp', position: 'aboveBar' },
    TRIM: { color: '#f0b429', shape: 'arrowDown', position: 'belowBar' },
    EXIT: { color: '#3fb950', shape: 'square', position: 'belowBar' },
    REVERSE: { color: '#ff7b72', shape: 'square', position: 'aboveBar' }
  };
  LightweightCharts.createSeriesMarkers(line, path.map(point => {
    const style = styles[point.change];
    const exposure = point.exposure == null ? '' : ` · ${Math.round(point.exposure * 100)}%`;
    return { time: point.time, ...style, text: `${point.change} ${point.quantity} → ${point.position > 0 ? '+' : ''}${point.position}${exposure}` };
  }));
  chart.timeScale().fitContent();
}
function setExecutionChartMode(mode) {
  if (!['trade', 'both', 'session'].includes(mode)) return;
  executionChartMode = mode;
  render();
}
function bindIndicatorControls(id, timeframe, mode) {
  document.querySelectorAll('[data-indicator]').forEach(input => input.onchange = () => {
    indicatorState[input.dataset.indicator] = input.checked; localStorage.setItem('tale-indicators', JSON.stringify(indicatorState));
    candleChartPreset = 'custom'; localStorage.setItem('tale-candle-preset', candleChartPreset); updateCandlePresetButtons();
    loadChart(id, timeframe, mode);
  });
}
async function loadChart(id, timeframe = '1m', mode = 'focus') {
  activeChartTimeframe = timeframe; activeChartMode = mode;
  const data = await api(`/api/v1/trades/${id}/chart?timeframe=${timeframe}${mode === 'full' ? '&view=full_session' : ''}`), el = $('#chart'), status = $('#chart-status');
  if (!el || !status) return;
  status.textContent = data.status + (data.source ? ` · ${data.source}` : ''); el.innerHTML = '';
  bindIndicatorControls(id, timeframe, mode);
  if (!data.bars?.length) return;
  const chart = LightweightCharts.createChart(el, {
    width: el.clientWidth,
    height: 600,
    layout: { background: { color: '#121d2d' }, textColor: '#d7e4f4' },
    localization: { timeFormatter: clockTime },
    timeScale: { timeVisible: true, secondsVisible: false, tickMarkFormatter: clockTime, borderColor: '#2b3a52' },
    rightPriceScale: { borderColor: '#2b3a52', scaleMargins: { top: .05, bottom: .35 } },
    grid: { vertLines: { color: '#2b3a52' }, horzLines: { color: '#2b3a52' } },
    crosshair: { vertLine: { color: '#67819f', labelBackgroundColor: '#253955' }, horzLine: { color: '#67819f', labelBackgroundColor: '#253955' } }
  });
  const candles = chart.addSeries(LightweightCharts.CandlestickSeries, {
    upColor: '#21c58f', downColor: '#ff8a5a',
    wickUpColor: '#21c58f', wickDownColor: '#ff8a5a',
    borderVisible: false
  });
  candles.setData(data.bars.map(bar => ({ time: Math.floor(bar.time / 1000), open: bar.open, high: bar.high, low: bar.low, close: bar.close })));
  const volume = chart.addSeries(LightweightCharts.HistogramSeries, { color: '#67819f', priceFormat: { type: 'volume' }, priceScaleId: 'vol' });
  volume.setData(data.bars.map(bar => ({ time: Math.floor(bar.time / 1000), value: bar.volume, color: '#67819f' })));
  chart.priceScale('vol').applyOptions({ scaleMargins: { top: .75, bottom: 0 }, borderVisible: false });
  const executionRows = executionPath(data.executions || []);
  const seconds = timeframe === '5m' ? 300 : 60, grouped = new Map();
  executionRows.forEach((execution, index) => {
    const time = Math.floor(execution.time / seconds) * seconds;
    if (!grouped.has(time)) grouped.set(time, []);
    grouped.get(time).push({ ...execution, number: index + 1 });
  });
  const markerStyle = { OPEN: ['#58a6ff', 'circle'], ADD: ['#bc8cff', 'arrowUp'], TRIM: ['#f0b429', 'arrowDown'], EXIT: ['#3fb950', 'square'], REVERSE: ['#ff7b72', 'square'] };
  const markers = [...grouped].map(([time, rows]) => {
    const roles = [...new Set(rows.map(row => row.change))], first = rows[0], last = rows[rows.length - 1];
    const number = rows.length === 1 ? `${first.number}` : `${first.number}–${last.number}`;
    const [color, shape] = markerStyle[last.change];
    return { time, position: last.action === 'buy' ? 'belowBar' : 'aboveBar', color, shape, text: `${number} ${roles.join('/')}` };
  });
  if (candleChartPreset !== 'executions' && data.excursion?.mfe_at > 0) markers.push({ time: Math.floor(data.excursion.mfe_at / 1e6 / seconds) * seconds, position: 'aboveBar', color: '#f0b429', shape: 'circle', text: 'MFE' });
  if (candleChartPreset !== 'executions' && data.excursion?.mae_at > 0) markers.push({ time: Math.floor(data.excursion.mae_at / 1e6 / seconds) * seconds, position: 'belowBar', color: '#ff7b72', shape: 'circle', text: 'MAE' });
  markers.sort((a, b) => a.time - b.time);
  LightweightCharts.createSeriesMarkers(candles, markers);
  const series = [['vwap', '#ffd166', 'vwap'], ['sma9', '#ff5d73', 'sma9'], ['sma20', '#5fb3ff', 'sma20'], ['ema9', '#c084fc', 'ema9'], ['ema20', '#f59e0b', 'ema20'], ['upper', '#8ca0bc', 'bollinger'], ['middle', '#67819f', 'bollinger'], ['lower', '#8ca0bc', 'bollinger']];
  const enabled = toggle => candleChartPreset === 'context' ? toggle === 'vwap' : candleChartPreset === 'custom' && indicatorState[toggle];
  for (const [name, color, toggle] of series) if (enabled(toggle) && data.indicators?.[name]?.length) { const line = chart.addSeries(LightweightCharts.LineSeries, { color, lineWidth: 2, lastValueVisible: false }); line.setData(data.indicators[name].map(point => ({ time: Math.floor(point.time / 1000), value: point.value }))); }
  if (candleChartPreset !== 'executions' && data.average_entry?.length) { const basis = chart.addSeries(LightweightCharts.LineSeries, { color: '#ffffff', lineWidth: 1, lineStyle: LightweightCharts.LineStyle.Dashed, lastValueVisible: false }); basis.setData(data.average_entry.map(point => ({ time: Math.floor(point.time / 1000), value: point.value }))); }
  if (candleChartPreset === 'executions' && mode !== 'full' && grouped.size) {
    const times = [...grouped.keys()], padding = Math.max(300, seconds * 2);
    chart.timeScale().setVisibleRange({ from: times[0] - padding, to: times[times.length - 1] + padding });
  } else {
    chart.timeScale().fitContent();
  }
  new ResizeObserver(entries => { const width = entries[0]?.contentRect.width; if (width) chart.applyOptions({ width }); }).observe(el);
}
function updateCandlePresetButtons() { document.querySelectorAll('[data-candle-preset]').forEach(button => button.classList.toggle('active', button.dataset.candlePreset === candleChartPreset)); }
function setCandleChartPreset(preset) {
  if (!['executions', 'context', 'custom'].includes(preset)) return;
  candleChartPreset = preset; localStorage.setItem('tale-candle-preset', preset); updateCandlePresetButtons();
  loadChart(selected, activeChartTimeframe, activeChartMode);
}
async function saveTrade(id) { await api(`/api/v1/trades/${id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ note: $('#note').value }) }); alert('Journal saved'); render(); }
function insertTradeTemplate() {
  const template = `Setup and market context:\n\nEntry trigger and invalidation:\n\nSize and risk decision:\n\nAdds / partials / trade management:\n\nExit quality and MFE capture:\n\nRule followed or broken:\n\nOne action to repeat or change:\n`;
  if (!$('#note').value.trim() || confirm('Replace the current trade note with the review template?')) $('#note').value = template;
}
async function addTradeTag(id) { const name = $('#new-tag').value.trim(); if (!name) return; await api(`/api/v1/trades/${id}/tags`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name, color: $('#new-tag-color').value }) }); render(); }
async function removeTradeTag(tradeId, tagId) { await api(`/api/v1/trades/${tradeId}/tags/${tagId}`, { method: 'DELETE' }); render(); }
async function editTag(id) { const tag = (await api('/api/v1/tags')).find(item => item.id === id); if (!tag) return; const name = prompt('Rename tag', tag.name); if (name == null || !name.trim()) return; await api(`/api/v1/tags/${id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: name.trim(), color: tag.color }) }); render(); }
async function deleteTag(id) { const tag = (await api('/api/v1/tags')).find(item => item.id === id); if (!tag || !confirm(`Delete “${tag.name}” from every trade?`)) return; await api(`/api/v1/tags/${id}`, { method: 'DELETE' }); render(); }
async function enrich(id) { try { const result = await api(`/api/v1/trades/${id}/enrich`, { method: 'POST' }); alert(`MFE ${money(result.mfe)} · MAE ${money(result.mae)} (${result.source}, ${result.completeness})`); render(); } catch (error) { alert(error.message); } }

async function calendar() {
  const year = calendarMonth.getFullYear(), month = calendarMonth.getMonth(), first = new Date(year, month, 1), last = new Date(year, month + 1, 0);
  const lead = first.getDay(), gridStart = new Date(year, month, 1 - lead), trail = 6 - last.getDay(), gridEnd = new Date(year, month + 1, trail);
  const range = calendarRange(localDate(gridStart), localDate(gridEnd));
  const days = range ? await api(`/api/v1/calendar?${range}`) : {};
  let total = 0, green = 0, red = 0, scratch = 0;
  for (let day = 1; day <= last.getDate(); day++) {
    const item = days[localDate(new Date(year, month, day))], net = item?.net || 0;
    if (item) { total += net; if (net > 10000) green++; else if (net < -10000) red++; else scratch++; }
  }
  let html = `<style>.day-grid{grid-template-columns:repeat(7,minmax(0,1fr)) minmax(135px,.8fr)}.week-total{min-height:82px;padding:10px;border:1px solid #30363d;border-radius:5px;background:#161b22;display:flex;flex-direction:column;justify-content:center}.week-total strong{font-size:17px}@media(max-width:850px){.day-grid{overflow-x:auto;grid-template-columns:repeat(7,minmax(105px,1fr)) minmax(135px,.8fr)}}</style><div class="toolbar"><button onclick="changeMonth(-1)">←</button><button onclick="calendarMonth=new Date();render()">Today</button><h2>${first.toLocaleString(undefined, { month: 'long', year: 'numeric' })}</h2><button onclick="changeMonth(1)">→</button></div><div class="day-grid">${['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Week P&L'].map(day => `<b>${day}</b>`).join('')}`;
  for (let date = new Date(gridStart); date <= gridEnd; date.setDate(date.getDate() + 1)) {
    const key = localDate(date), inMonth = date.getMonth() === month, included = dateIsInRange(key), item = days[key], net = item?.net || 0;
    html += inMonth ? `<div class="day ${included ? `clickable ${net > 10000 ? 'positive' : net < -10000 ? 'negative' : 'scratch'}` : 'filtered-out'}"${included ? ` onclick="showDay('${key}')"` : ''}><b>${date.getDate()}</b>${item ? `<br>${money(net)}<br><small>${item.trades} trades</small>` : ''}</div>` : '<div></div>';
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
  const rangeNote = dateStart || dateEnd ? 'Calendar totals and day drill-downs respect the selected date range.' : 'Weekly P&L uses complete Sunday–Saturday calendar weeks; boundary weeks can include adjacent-month trading days.';
  return `${filters()}<div class="cards">${metric(`${first.toLocaleString(undefined, { month: 'long' })} P&L`, money(total), total >= 0 ? 'green' : 'red')}${metric('Trading days', green + red + scratch)}${metric('Green / red / scratch', `${green} / ${red} / ${scratch}`)}</div>${html}</div><p class="muted">${rangeNote}</p>`;
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
  setTimeout(() => drawEquity(equity, drawdown, '#day-equity-chart', true), 0);
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

async function settings() { const settings = await api('/api/v1/settings'), tags = await api('/api/v1/tags'); const timezone = settings.app.Timezone || settings.app.timezone, tolerance = settings.import.ScratchTolerance ?? settings.import.scratch_tolerance, timeframe = settings.chart.DefaultTimeframe || settings.chart.default_timeframe, polygonURL = settings.chart.PolygonChartsURL || settings.chart.polygon_charts_url || 'http://localhost:8081'; return `<h2>Settings & privacy</h2><section class="panel"><div class="toolbar"><label>Timezone <input id="setting-timezone" value="${esc(timezone)}"></label><label>Scratch tolerance <input id="setting-tolerance" type="number" min="0" step="0.01" value="${tolerance}"></label><label>Default chart <select id="setting-timeframe"><option value="1m" ${timeframe === '1m' ? 'selected' : ''}>1 minute</option><option value="5m" ${timeframe === '5m' ? 'selected' : ''}>5 minute</option></select></label><label>Polygon Charts URL <input id="setting-polygon-url" type="url" value="${esc(polygonURL)}" placeholder="http://localhost:8081"></label><button onclick="saveSettings()">Save settings</button></div><p>Database: ${esc(settings.storage.path)} · Massive: ${settings.massive_configured ? 'configured' : 'not configured'} · Polygon Charts: ${esc(polygonURL)}. The API key never reaches this browser.</p><button onclick="backup()">Create local backup</button><h3>Tags</h3><div class="tag-list tag-manager">${tags.map(tag => `<span class="tag"><span style="background:${esc(tag.color)}"></span>${esc(tag.name)}<button onclick="editTag(${tag.id})">Edit</button><button class="danger" onclick="deleteTag(${tag.id})">Delete</button></span>`).join('') || '<span class="muted">Tags appear here after they are assigned to a trade.</span>'}</div><p class="muted">Deleting a tag removes it from every trade. A tag removed from its final trade is deleted automatically.</p><h3 id="privacy">Privacy</h3><p>Thinkorswim imports, notes, tags and analytics remain in your local SQLite database. Only a symbol and historical interval are sent to Massive for requested market data. Nothing is sent to TradeTally or telemetry services.</p></section>`; }
async function saveSettings() { await api('/api/v1/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ timezone: $('#setting-timezone').value.trim(), scratch_tolerance: Number($('#setting-tolerance').value), default_timeframe: $('#setting-timeframe').value, polygon_charts_url: $('#setting-polygon-url').value.trim() }) }); alert('Settings saved locally.'); render(); }
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
try { const saved = JSON.parse(localStorage.getItem('tale-cohort') || '{}'); cohortSymbol = saved.symbol || ''; cohortDirection = saved.direction || ''; cohortOutcome = saved.outcome || ''; cohortHolding = saved.holding || ''; } catch (_) {}
window.addEventListener('hashchange', () => { applyRoute(); render(); });
applyRoute();
render();
