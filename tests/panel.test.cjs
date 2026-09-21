const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const { webcrypto } = require('node:crypto');
const source = fs.readFileSync(`${__dirname}/../assets/panel.js`, 'utf8');
const html = fs.readFileSync(`${__dirname}/../assets/index.html`, 'utf8');
const tick = () => new Promise(resolve => setImmediate(resolve));

// Run the shipped script, including its event wiring, with controllable HTTP
// completion order. Layout is checked separately in the browser at native size.
async function panel() {
  const elements = new Map(), requests = [], windowEvents = {}, documentEvents = {};
  class Element {
    constructor(tag = 'div') {
      this.tag = tag; this.hidden = false; this.value = ''; this.children = [];
      this.dataset = {}; this.attributes = {}; this.events = {}; this.classes = new Set();
      this.style = {setProperty:(k,v) => this.style[k] = v, getPropertyValue:k => this.style[k], removeProperty:k => delete this.style[k]};
      this.classList = {add:c=>this.classes.add(c),remove:c=>this.classes.delete(c),contains:c=>this.classes.has(c),toggle:(c,on)=>on ? this.classes.add(c) : this.classes.delete(c)};
    }
    set textContent(value) { this.text = value; this.children = []; }
    get textContent() { return (this.text || '') + this.children.map(x=>x.textContent).join(''); }
    set className(value) { this.classes = new Set(value.split(' ')); }
    get className() { return [...this.classes].join(' '); }
    setAttribute(k,v) { this.attributes[k] = v; }
    removeAttribute(k) { delete this.attributes[k]; }
    replaceChildren(...children) { this.text = ''; this.children = children; }
    append(...children) { this.children.push(...children); }
    addEventListener(name, callback) { this.events[name] = callback; }
    focus() { document.activeElement = this; }
    select() { this.focus(); }
    scrollIntoView() {}
    closest(selector) {
      if (selector === '#notice button') return this.inNotice ? this : null;
      return ['button','select','textarea'].includes(this.tag) ? this : null;
    }
  }
  for (const match of html.matchAll(/<(\w+)[^>]*\bid="([^"]+)"[^>]*>/g)) {
    const element = new Element(match[1]); element.hidden = /\bhidden\b/.test(match[0]); elements.set(match[2], element);
  }
  const document = {getElementById:id=>elements.get(id),createElement:tag=>new Element(tag),body:new Element(),documentElement:new Element(),readyState:'complete',addEventListener:(n,cb)=>documentEvents[n]=cb};
  const context = vm.createContext({document, window:{addEventListener:(n,cb)=>windowEvents[n]=cb}, navigator:{platform:'MacIntel'}, crypto:{getRandomValues:array=>webcrypto.getRandomValues(array)},
    localStorage:{getItem:()=>null,setItem(){}},setTimeout:()=>1,clearTimeout(){},Option:function(text,value){this.textContent=text;this.value=value;},
    fetch(path, options) { return new Promise((resolve,reject)=>requests.push({path,body:options.body && JSON.parse(options.body),resolve:data=>resolve({ok:true,json:async()=>data}),reject})); }
  });
  vm.runInContext(source, context);
  const run = code => vm.runInContext(code, context);
  const take = path => { const i=requests.findIndex(r=>r.path===path); assert.notEqual(i,-1,`missing ${path}`); return requests.splice(i,1)[0]; };
  take('/api/state').resolve({profile:'A',region:'eu-west-1',theme:'light',profiles:[{name:'A'},{name:'B'}]});
  await tick();
  return {run,take,requests,elements,windowEvents,documentEvents};
}

test('late state responses cannot replace a newer profile or its active badge', async()=>{
  const h=await panel(); const old=h.run('load()'); const a=h.take('/api/state'); const fresh=h.run('load()'); const b=h.take('/api/state');
  b.resolve({profile:'B',profiles:[{name:'A',is_active:true},{name:'B',is_active:false}]}); await fresh;
  a.resolve({profile:'A',profiles:[]}); await old;
  assert.equal(h.elements.get('currentName').textContent,'B');
  assert.equal(h.run('rows[1].element.textContent'),'BActive');
  assert.equal(h.run('rows[0].element.textContent'),'A');
});

test('late failed refresh cannot erase newer state', async()=>{
  const h=await panel(); const old=h.run('load()'); const a=h.take('/api/state'); const fresh=h.run('load()');
  h.take('/api/state').resolve({profile:'B',profiles:[]}); await fresh; a.reject(new Error('offline')); await old;
  assert.equal(h.run('state.profile'),'B');
});

test('whoami pins its profile and rejects a mismatched identity', async()=>{
  const h=await panel(); const result=h.run('loadIdentity()'); const request=h.take('/api/whoami');
  assert.equal(request.body.profile,'A'); request.resolve({profile:'B',arn:'wrong-arn'}); await result;
  assert.match(h.elements.get('identityText').textContent,/Check again/);
  assert.doesNotMatch(h.elements.get('identityText').textContent,/wrong-arn/);
});

test('an identity requested before a switch never appears under the new profile', async()=>{
  const h=await panel(); const result=h.run('loadIdentity()'); const request=h.take('/api/whoami');
  const refresh=h.run('load()'); h.take('/api/state').resolve({profile:'B',profiles:[]}); await refresh;
  request.resolve({profile:'A',arn:'old-arn'}); await result;
  assert.equal(h.elements.get('identityText').textContent,''); assert.equal(h.elements.get('identityButton').disabled,false);
});

test('focus preserves progress and Cancel, which targets only this console operation', async()=>{
  const h=await panel(); h.run('openConsole({name:"B"})'); const consoleRequest=h.take('/api/console');
  h.windowEvents.focus(); await tick();
  assert.equal(h.requests.length,0); assert.match(h.elements.get('notice').textContent,/Opening the console.*Cancel/);
  const cancel=h.run('cancelRunning()'); const cancelRequest=h.take('/api/cancel');
  assert.equal(cancelRequest.body.operationId,consoleRequest.body.operationId);
  cancelRequest.resolve({cancelled:true}); await cancel;
  consoleRequest.resolve({cancelled:true}); await tick();
  h.take('/api/state').resolve({profile:'A',profiles:[]}); await tick();
  assert.equal(h.requests.some(r=>r.path==='/api/hide'),false);
  assert.equal(h.run('document.body.classList.contains("busy")'),false);
  assert.match(h.elements.get('notice').textContent,/Cancelled/);
});

test('an action invalidates pending state requests and rejects a second action', async()=>{
  const h=await panel(); const old=h.run('load()'); const stale=h.take('/api/state');
  h.run('activate({name:"B"}); activate({name:"A"})'); assert.equal(h.requests.length,1);
  const action=h.take('/api/profile/set'); stale.resolve({profile:'stale',profiles:[]}); await old;
  assert.equal(h.run('state.profile'),'A'); action.resolve({}); await tick();
  h.take('/api/state').resolve({profile:'B',profiles:[]}); await tick(); assert.equal(h.run('state.profile'),'B');
});

test('rapid preference changes merge after each confirmed save', async()=>{
  const h=await panel(); h.run('prefs={theme:"light",browser:"default"}');
  const first=h.run('saveSettings({theme:"dark"})'), second=h.run('saveSettings({browser:"firefox"})');
  await tick(); assert.equal(h.requests.length,1); const a=h.take('/api/settings'); a.resolve(a.body); await first; await tick();
  const b=h.take('/api/settings'); assert.equal(b.body.theme,'dark'); assert.equal(b.body.browser,'firefox'); b.resolve(b.body); await second;
  assert.equal(h.elements.get('themeSelect').value,'dark'); assert.equal(h.elements.get('browserSelect').value,'firefox');
});

test('a settings read started before a save cannot undo it', async()=>{
  const h=await panel(); const read=h.run('loadSettings()'); const old=h.take('/api/settings');
  const saved=h.run('saveSettings({theme:"dark"})'); await tick(); const write=h.take('/api/settings'); write.resolve(write.body); await saved;
  old.resolve({theme:'light'}); await read; assert.equal(h.run('prefs.theme'),'dark');
});

test('a failed save does not poison later saves or reuse rejected preferences', async()=>{
  const h=await panel(); const first=h.run('saveSettings({theme:"dark"})'), second=h.run('saveSettings({browser:"zen"})'); await tick();
  h.take('/api/settings').resolve({error:'disk full'}); await tick(); h.take('/api/settings').resolve({theme:'light',browser:'default'}); await first; await tick();
  const next=h.take('/api/settings'); assert.equal(next.body.theme,'light'); assert.equal(next.body.browser,'zen'); next.resolve(next.body); await second;
});

test('clear sends the profile visible when clicked', async()=>{
  const h=await panel(); const clear=h.run('clearActive()'); const request=h.take('/api/clear'); assert.equal(request.body.profile,'A');
  request.resolve({error:'active profile changed'}); await tick(); h.take('/api/state').resolve({profile:'B',profiles:[]}); await clear;
  assert.equal(h.run('state.profile'),'B'); assert.match(h.elements.get('notice').textContent,/active profile changed/);
});

test('native region select owns ArrowDown and Enter instead of switching a profile', async()=>{
  const h=await panel(); const before=h.run('selected');
  for (const key of ['ArrowDown','Enter']) h.documentEvents.keydown({key,target:h.elements.get('regionSelect'),preventDefault(){assert.fail('intercepted native control');}});
  assert.equal(h.run('selected'),before); assert.equal(h.requests.length,0);
});

test('busy panel allows keyboard activation of Cancel', async()=>{
  const h=await panel(); h.run('openConsole({name:"B"})');
  const button=h.elements.get('notice').children[1]; button.inNotice=true;
  h.documentEvents.keydown({key:'Enter',target:button,preventDefault(){assert.fail('blocked Cancel');}});
  h.documentEvents.keydown({key:'Tab',target:h.elements.get('search'),preventDefault(){assert.fail('blocked focus navigation');}});
});


test('keyboard selection is exposed to assistive technology and clears with empty search results', async()=>{
  const h=await panel();
  h.documentEvents.keydown({key:'ArrowDown',target:h.elements.get('search'),preventDefault(){}});
  assert.equal(h.run('rows[1].element.attributes["aria-selected"]'),'true');
  assert.equal(h.elements.get('search').attributes['aria-activedescendant'],'profile-1');
  h.elements.get('search').value='missing'; h.elements.get('search').events.input();
  assert.equal(h.elements.get('search').attributes['aria-activedescendant'],undefined);
  assert.match(h.elements.get('list').textContent,/No match/);
});
