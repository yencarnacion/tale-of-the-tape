const pages = await fetch('http://127.0.0.1:9223/json').then(response => response.json());
const page = pages.find(item => item.type === 'page');
if (!page) throw new Error('No browser page found');

const socket = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((resolve, reject) => {
  socket.addEventListener('open', resolve, { once: true });
  socket.addEventListener('error', reject, { once: true });
});

let id = 0;
const pending = new Map();
socket.addEventListener('message', event => {
  const message = JSON.parse(event.data);
  if (!message.id || !pending.has(message.id)) return;
  const { resolve, reject } = pending.get(message.id);
  pending.delete(message.id);
  if (message.error) reject(new Error(message.error.message));
  else resolve(message.result);
});
function command(method, params = {}) {
  const requestID = ++id;
  socket.send(JSON.stringify({ id: requestID, method, params }));
  return new Promise((resolve, reject) => pending.set(requestID, { resolve, reject }));
}
async function evaluate(expression) {
  const result = await command('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text);
  return result.result.value;
}

await new Promise(resolve => setTimeout(resolve, 1500));
if (process.argv[2] === 'chart-clarity') {
  await evaluate(`showTrade(629)`);
  await new Promise(resolve => setTimeout(resolve, 3500));
  const oneMinute = await evaluate(`({canvas: Boolean(document.querySelector('#chart canvas')), preset: candleChartPreset, help: document.querySelector('.chart-help')?.textContent, fatal: document.querySelector('#app > .panel.red')?.textContent || ''})`);
  await evaluate(`loadChart(629, '5m')`);
  await new Promise(resolve => setTimeout(resolve, 2500));
  const fiveMinute = await evaluate(`({canvas: Boolean(document.querySelector('#chart canvas')), timeframe: activeChartTimeframe, fatal: document.querySelector('#app > .panel.red')?.textContent || ''})`);
  await evaluate(`loadChart(629, '1m', 'full')`);
  await new Promise(resolve => setTimeout(resolve, 2500));
  const fullSession = await evaluate(`({canvas: Boolean(document.querySelector('#chart canvas')), mode: activeChartMode, fatal: document.querySelector('#app > .panel.red')?.textContent || ''})`);
  console.log(JSON.stringify({ oneMinute, fiveMinute, fullSession }, null, 2));
  socket.close();
  process.exit(0);
}
if (process.argv[2] === 'day') {
  await evaluate(`showDay('2026-07-21')`);
  await new Promise(resolve => setTimeout(resolve, 2500));
  const day = await evaluate(`({
    text: document.querySelector('#app').innerText,
    chart: Boolean(document.querySelector('#day-equity-chart canvas')),
    tradeRows: document.querySelectorAll('#app tbody tr').length,
    errors: document.querySelector('#app .red')?.textContent || ''
  })`);
  console.log(JSON.stringify(day, null, 2));
  socket.close();
  process.exit(0);
}
if (process.argv[2] === 'detail') {
  await evaluate(`showTrade(623)`);
  await new Promise(resolve => setTimeout(resolve, 3500));
  const detail = await evaluate(`({
    text: document.querySelector('#app').innerText.slice(0, 1600),
    chart: Boolean(document.querySelector('#chart canvas')),
    executionRows: document.querySelectorAll('#app .detail tbody tr').length,
    fatal: document.querySelector('#app > .panel.red')?.textContent || ''
  })`);
  console.log(JSON.stringify(detail, null, 2));
  socket.close();
  process.exit(0);
}
if (process.argv[2] === 'history') {
  await evaluate(`navigate('calendar')`);
  await new Promise(resolve => setTimeout(resolve, 1200));
  await evaluate(`showDay('2026-07-21')`);
  await new Promise(resolve => setTimeout(resolve, 1200));
  await evaluate(`showTrade(623)`);
  await new Promise(resolve => setTimeout(resolve, 1200));
  const trade = await evaluate(`({hash: location.hash, view, selected, selectedDay})`);
  await evaluate(`history.back()`);
  await new Promise(resolve => setTimeout(resolve, 1200));
  const day = await evaluate(`({hash: location.hash, view, selected, selectedDay, heading: document.querySelector('#app h2')?.textContent})`);
  await evaluate(`history.back()`);
  await new Promise(resolve => setTimeout(resolve, 1200));
  const calendar = await evaluate(`({hash: location.hash, view, heading: document.querySelector('#app h2')?.textContent})`);
  await evaluate(`history.forward()`);
  await new Promise(resolve => setTimeout(resolve, 1200));
  const forward = await evaluate(`({hash: location.hash, view, selectedDay})`);
  console.log(JSON.stringify({ trade, day, calendar, forward }, null, 2));
  socket.close();
  process.exit(0);
}
const before = await evaluate(`({text: document.querySelector('#app').innerText.slice(0, 300), start: dateStart, end: dateEnd})`);
await evaluate(`setDatePreset('week')`);
await new Promise(resolve => setTimeout(resolve, 1500));
const after = await evaluate(`({
  text: document.querySelector('#app').innerText.slice(0, 500),
  html: document.querySelector('#app').innerHTML.slice(0, 500),
  start: dateStart,
  end: dateEnd
})`);
console.log(JSON.stringify({ before, after }, null, 2));
socket.close();
