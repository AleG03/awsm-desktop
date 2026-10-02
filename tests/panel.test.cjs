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
async function panel(initialState = {profile:'A',region:'eu-west-1',theme:'light',profiles:[{name:'A'},{name:'B'}]}, storage = new Map(), {grant = width => width} = {}) {
  const elements = new Map(), requests = [], resizes = [], windowEvents = {}, documentEvents = {};
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
    remove() {}
    // A fixed-pitch stand-in for the system font: seven points a character.
    getBoundingClientRect() { return {width: this.textContent.length * 7, height: 18, left: 0}; }
    closest(selector) {
      if (selector === '#notice button') return this.inNotice ? this : null;
      return ['button','select','textarea'].includes(this.tag) ? this : null;
    }
  }
  for (const match of html.matchAll(/<(\w+)[^>]*\bid="([^"]+)"[^>]*>/g)) {
    const element = new Element(match[1]); element.hidden = /\bhidden\b/.test(match[0]); elements.set(match[2], element);
  }
  const document = {getElementById:id=>elements.get(id),createElement:tag=>new Element(tag),body:new Element(),documentElement:new Element(),readyState:'complete',addEventListener:(n,cb)=>documentEvents[n]=cb};
  // Rows and the list are padded 8 a side with 10 between columns, the pane 8
  // and 6 with a 1 point border, as in panel.css.
  const getComputedStyle = element => element === elements.get('sessions')
    ? {paddingLeft:'8px', paddingRight:'6px', borderLeftWidth:'0px', borderRightWidth:'1px'}
    : {paddingLeft:'8px', paddingRight:'8px', columnGap:'10px'};
  const context = vm.createContext({document, getComputedStyle, window:{addEventListener:(n,cb)=>windowEvents[n]=cb}, navigator:{platform:'MacIntel'}, crypto:{getRandomValues:array=>webcrypto.getRandomValues(array)},
    localStorage:{getItem:key=>storage.get(key) ?? null,setItem:(key,value)=>storage.set(key,value)},setTimeout:()=>1,clearTimeout(){},Option:function(text,value){this.textContent=text;this.value=value;},
    fetch(path, options) {
      const body = options.body && JSON.parse(options.body);
      // Answered at once, and kept apart, so that no other test has to step
      // over the panel fitting itself to its names.
      if (path === '/api/resize') {
        resizes.push(body.width);
        try { const width = grant(body.width); return Promise.resolve({ok:true,json:async()=>({width})}); }
        catch (error) { return Promise.reject(error); }
      }
      return new Promise((resolve,reject)=>requests.push({path,body,resolve:data=>resolve({ok:true,json:async()=>data}),reject}));
    }
  });
  vm.runInContext(source, context);
  const run = code => vm.runInContext(code, context);
  const take = path => { const i=requests.findIndex(r=>r.path===path); assert.notEqual(i,-1,`missing ${path}`); return requests.splice(i,1)[0]; };
  take('/api/state').resolve(initialState);
  await tick();
  return {run,take,requests,resizes,elements,windowEvents,documentEvents};
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

const sessionState = {
  profile: 'outside', theme: 'light', profiles: [
    {name:'outside',sso_session:'team-prod'},
    {name:'dev-admin',sso_session:'team',account_id:'123',sso_role_name:'Admin'},
    {name:'dev-reader',sso_session:'team',account_id:'456',sso_role_name:'ReadOnly'},
    {name:'upper',sso_session:'Team'},
    {name:'static'},
  ],
};
const rowNames = h => Array.from(h.run('rows.map(row => row.profile.name)'));
const sessionButton = (h, session) => h.run(`sessionButtons.find(b => b.session === ${JSON.stringify(session)})`).element;
const sessionNames = h => Array.from(h.run('sessionButtons.map(b => b.session)'));
const chosenSession = h => h.run('selectedSession');
function chooseSession(h, session) {
  sessionButton(h, session).events.click();
}
function search(h, text) {
  h.elements.get('search').value = text; h.elements.get('search').events.input();
}
function key(h, key, target = 'search', preventDefault = () => {}) {
  h.documentEvents.keydown({key,target:h.elements.get(target),preventDefault});
}

test('session filter uses exact equality, combines all search terms and leaves active header alone', async()=>{
  const h=await panel(sessionState);
  assert.equal(rowNames(h).includes('static'),true);
  key(h,'ArrowDown');
  search(h,'admin 123');
  chooseSession(h,'team');
  assert.deepEqual(rowNames(h),['dev-admin']);
  assert.equal(h.elements.get('search').value,'admin 123');
  assert.equal(h.run('selected'),0);
  assert.equal(h.run('document.activeElement === ui.search'),true);
  assert.equal(h.elements.get('currentName').textContent,'outside');
  assert.equal(h.run('state.profile'),'outside');
  assert.equal(h.requests.length,0);
  search(h,'');
  assert.deepEqual(rowNames(h),['dev-admin','dev-reader']);
  chooseSession(h,'Team');
  assert.deepEqual(rowNames(h),['upper']);
  chooseSession(h,'');
  assert.equal(rowNames(h).includes('static'),true);
});

test('sessions come from all profiles, stay unique and sorted, and hide only when redundant', async()=>{
  const h=await panel(sessionState);
  const pane=h.elements.get('sessions');
  const expected=[...new Set(sessionState.profiles.map(p=>p.sso_session).filter(Boolean))].sort((a,b)=>a.localeCompare(b));
  assert.deepEqual(sessionNames(h),['',...expected]);
  assert.deepEqual(pane.children.map(button=>button.children[0].textContent),['All sessions',...expected]);
  search(h,'no results');
  assert.equal(pane.children.length,4);
  assert.equal(pane.hidden,false);
  for (const [profiles,hidden] of [
    [[],true],[[{name:'static'}],true],
    [[{name:'one',sso_session:'only'},{name:'two',sso_session:'only'}],true],
    [[{name:'one',sso_session:'only'},{name:'static'}],false],
  ]) {
    const refreshed=h.run('load()'); h.take('/api/state').resolve({profiles}); await refreshed;
    assert.equal(pane.hidden,hidden);
  }
});

test('recent profiles obey the session scope and keep existing grouping', async()=>{
  const storage=new Map([['awsm.recents',JSON.stringify(['outside','dev-reader','static'])]]);
  const h=await panel(sessionState,storage);
  chooseSession(h,'team');
  assert.deepEqual(rowNames(h),['dev-reader','dev-admin']);
  assert.match(h.elements.get('list').textContent,/Recent.*dev-reader.*team.*dev-admin/);
  assert.doesNotMatch(h.elements.get('list').textContent,/outside|static/);
  search(h,'dev');
  assert.deepEqual(rowNames(h),['dev-admin','dev-reader']);
  assert.doesNotMatch(h.elements.get('list').textContent,/Recent/);
});

test('empty filtered results clear accessible selection and Escape clears query, scope, then hides', async()=>{
  const h=await panel(sessionState);
  chooseSession(h,'team'); search(h,'outside');
  assert.deepEqual(rowNames(h),[]);
  assert.match(h.elements.get('list').textContent,/No match/);
  assert.equal(h.elements.get('search').attributes['aria-activedescendant'],undefined);
  key(h,'Escape');
  assert.equal(h.elements.get('search').value,'');
  assert.equal(chosenSession(h),'team');
  assert.deepEqual(rowNames(h),['dev-admin','dev-reader']);
  assert.equal(h.requests.length,0);
  key(h,'Escape');
  assert.equal(chosenSession(h),'');
  assert.equal(h.run('selected'),0);
  assert.equal(rowNames(h).length,5);
  assert.equal(h.requests.length,0);
  key(h,'Escape');
  h.take('/api/hide').resolve({});
});

test('in the session pane the arrows move between sessions, return is the button\'s, and profile shortcuts do nothing', async()=>{
  const h=await panel(sessionState);
  chooseSession(h,'team');
  const sessions=sessionNames(h), at=sessions.indexOf('team');
  const onSession=(pressed,extra={})=>h.documentEvents.keydown({key:pressed,target:h.run('document.activeElement'),...extra});
  sessionButton(h,'team').focus();
  onSession('ArrowDown',{preventDefault(){}});
  assert.equal(chosenSession(h),sessions[at+1]);
  assert.equal(h.run('document.activeElement === sessionButtons[' + (at+1) + '].element'),true);
  onSession('ArrowUp',{preventDefault(){}}); onSession('ArrowUp',{preventDefault(){}});
  assert.equal(chosenSession(h),sessions[at-1]);
  for (const pressed of ['Enter',' ','f','c','t','k']) {
    onSession(pressed,{metaKey:true,preventDefault(){assert.fail(`intercepted ${pressed} on a session`);}});
  }
  assert.equal(h.requests.length,0);
  onSession('d',{preventDefault(){assert.fail('typing was swallowed');}});
  assert.equal(h.run('document.activeElement === ui.search'),true);
});

test('only the chosen session is in the tab order, and counts follow the search', async()=>{
  const h=await panel(sessionState);
  const tabbable=()=>h.run('sessionButtons.filter(b => b.element.tabIndex === 0).map(b => b.session)');
  assert.deepEqual(Array.from(tabbable()),['']);
  chooseSession(h,'team');
  assert.deepEqual(Array.from(tabbable()),['team']);
  assert.equal(sessionButton(h,'team').attributes['aria-pressed'],'true');
  assert.equal(sessionButton(h,'').attributes['aria-pressed'],'false');
  const count=session=>sessionButton(h,session).children[1].textContent;
  assert.deepEqual([count(''),count('team'),count('Team')],['5','2','1']);
  search(h,'reader');
  assert.deepEqual([count(''),count('team'),count('Team')],['1','1','0']);
  assert.equal(sessionButton(h,'Team').classes.has('none'),true);
  assert.equal(sessionButton(h,'team').classes.has('none'),false);
});

test('filter survives hiding and reopening but a fresh app resets without persisting it', async()=>{
  const storage=new Map(); const h=await panel(sessionState,storage);
  chooseSession(h,'team');
  h.run('dismiss()'); h.take('/api/hide').resolve({});
  h.windowEvents.focus(); h.take('/api/state').resolve(sessionState); await tick();
  assert.equal(chosenSession(h),'team');
  assert.deepEqual(rowNames(h),['dev-admin','dev-reader']);
  assert.equal(storage.size,0);
  const restarted=await panel(sessionState,storage);
  assert.equal(chosenSession(restarted),'');
  assert.equal(rowNames(restarted).length,5);
});

test('only a successful current refresh updates options and resets a removed session', async()=>{
  const h=await panel(sessionState); chooseSession(h,'team'); search(h,'dev');
  const failed=h.run('load()'); h.take('/api/state').reject(new Error('offline'));
  await assert.rejects(failed,/offline/);
  assert.equal(chosenSession(h),'team');
  const old=h.run('load()'); const stale=h.take('/api/state');
  const fresh=h.run('load()'); h.take('/api/state').resolve(sessionState); await fresh;
  stale.resolve({profiles:[]}); await old;
  assert.equal(chosenSession(h),'team');
  const removed=h.run('load()');
  h.take('/api/state').resolve({profiles:[{name:'dev-new',sso_session:'new'}, {name:'static'}]}); await removed;
  assert.equal(chosenSession(h),'');
  assert.deepEqual(sessionNames(h),['','new']);
  assert.equal(h.elements.get('search').value,'dev');
  assert.deepEqual(rowNames(h),['dev-new']);
});

test('session pane stays disabled through action refresh and recovers after failure or cancellation', async()=>{
  const disabled=h=>Array.from(h.run('sessionButtons.map(b => b.element.disabled)'));
  for (const result of [{error:'login failed'},{cancelled:true}]) {
    const h=await panel(sessionState); chooseSession(h,'team');
    h.run('activate({name:"dev-admin"})'); const request=h.take('/api/profile/set');
    assert.equal(disabled(h).every(Boolean),true);
    chooseSession(h,'team-prod');
    sessionButton(h,'team').focus();
    h.documentEvents.keydown({key:'ArrowDown',target:sessionButton(h,'team'),preventDefault(){}});
    assert.equal(chosenSession(h),'team');
    assert.deepEqual(rowNames(h),['dev-admin','dev-reader']);
    request.resolve(result); await tick();
    assert.equal(disabled(h).every(Boolean),true);
    h.take('/api/state').resolve(sessionState); await tick();
    assert.equal(disabled(h).some(Boolean),false);
    assert.equal(chosenSession(h),'team');
  }
});

// --- keys and clicks --------------------------------------------------------

async function settingsOpen(initial) {
  const h=await panel(initial); h.run('showSettings(true)');
  h.take('/api/settings').resolve({theme:'light',browser:'default',bindings:{}}); await tick();
  return h;
}
const press = (h, event) => h.documentEvents.keydown({target:h.elements.get('search'),preventDefault(){},...event});

test('⌘-click opens the clicked row in a Firefox container, not the selected one', async()=>{
  const h=await panel();
  assert.equal(h.run('current().name'),'A');
  h.run('rows[1].element').events.click({metaKey:true,detail:1});
  const request=h.take('/api/console');
  assert.equal(request.body.profile,'B'); assert.equal(request.body.browser,'firefox');
});

test('a plain click still switches to the clicked row, and ⌘⇧↵ is no longer bound', async()=>{
  const h=await panel();
  press(h,{key:'Enter',metaKey:true,shiftKey:true,preventDefault(){assert.fail('⌘⇧↵ is still bound');}});
  assert.equal(h.requests.length,0);
  h.run('rows[1].element').events.click({detail:1});
  assert.equal(h.take('/api/profile/set').body.name,'B');
});

test('the default keys reach their actions on the selected profile', async()=>{
  for (const [event,path,check] of [
    [{key:'Enter'},'/api/profile/set',b=>b.name==='A'],
    [{key:'Enter',metaKey:true},'/api/console',b=>b.profile==='A'&&b.browser===''],
    [{key:'f',metaKey:true},'/api/console',b=>b.browser==='firefox'],
    [{key:'t',ctrlKey:true},'/api/terminal',b=>b.profile==='A'],
    [{key:'C',metaKey:true,shiftKey:true},'/api/copy',b=>b.text==='A'],
    [{key:'k',metaKey:true},'/api/clear',b=>b.profile==='A'],
  ]) {
    const h=await panel(); let prevented=false;
    press(h,{...event,preventDefault(){prevented=true;}});
    assert.equal(prevented,true,JSON.stringify(event)); assert.ok(check(h.take(path).body),JSON.stringify(event));
  }
});

test('bindings from the settings file replace the defaults they took over', async()=>{
  const custom={profile:'A',profiles:[{name:'A'},{name:'B'}],bindings:{firefox:['Alt+Click'],terminal:['CmdOrCtrl+T','CmdOrCtrl+F']}};
  let h=await panel(custom);
  h.run('rows[1].element').events.click({metaKey:true,detail:1});
  assert.equal(h.requests.length,0);
  h.run('rows[1].element').events.click({altKey:true,detail:1});
  assert.equal(h.take('/api/console').body.browser,'firefox');
  h=await panel(custom); press(h,{key:'f',metaKey:true});
  assert.equal(h.take('/api/terminal').body.profile,'A');
});

test('a hand-edited binding that would take a letter from the search field is ignored', async()=>{
  const h=await panel({profile:'A',profiles:[{name:'A'}],bindings:{terminal:['A','CmdOrCtrl+T']}});
  press(h,{key:'a',preventDefault(){assert.fail('took a letter from search');}});
  assert.equal(h.requests.length,0);
  assert.deepEqual(Array.from(h.run('bindings().terminal')),['CmdOrCtrl+T']);
});

test('the footer names the keys as they are bound now', async()=>{
  assert.equal((await panel()).elements.get('hint').textContent,'↵ Switch · ⌘↵ Console · Right click for more');
  const h=await panel({profile:'A',profiles:[{name:'A'}],bindings:{switch:['CmdOrCtrl+Shift+Enter'],console:[]}});
  assert.equal(h.elements.get('hint').textContent,'⌘⇧↵ Switch · Right click for more');
});

test('recording a key moves it from the action that had it and stores only what changed', async()=>{
  const h=await settingsOpen();
  h.run('addButtons.get("terminal")').events.click({detail:1});
  assert.equal(h.run('recordingAction'),'terminal');
  press(h,{key:'c',metaKey:true});
  assert.equal(h.run('recordingAction'),null);
  assert.match(h.elements.get('bindingsNote').textContent,/⌘C moved here from “Copy the account id”/);
  await tick(); const save=h.take('/api/settings');
  assert.deepEqual(save.body.bindings,{copyAccount:[],terminal:['CmdOrCtrl+T','CmdOrCtrl+C']});
  save.resolve(save.body); await tick();
  assert.equal(h.run('actionFor("CmdOrCtrl+C").id'),'terminal');
  assert.equal(h.elements.get('bindingsReset').hidden,false);
});

test('keys that belong to typing or moving around are refused, and Escape only cancels', async()=>{
  const h=await settingsOpen(); h.run('startBinding("console")');
  for (const [event,pattern] of [
    [{key:'a'},/search field/],[{key:'A',shiftKey:true},/search field/],
    [{key:'Tab'},/move around/],[{key:'ArrowDown'},/move through/],
  ]) {
    press(h,event);
    assert.match(h.elements.get('bindingsNote').textContent,pattern);
    assert.equal(h.run('recordingAction'),'console');
  }
  press(h,{key:'Escape'});
  assert.equal(h.run('recordingAction'),null);
  assert.equal(h.elements.get('settings').hidden,false);
  await tick(); assert.equal(h.requests.length,0);
});

test('a click is recorded with its modifiers, but not a key press or the second half of a double click', async()=>{
  const h=await settingsOpen(); const add=h.run('addButtons.get("copyName")');
  add.events.click({detail:1}); add.events.click({detail:2}); add.events.click({detail:0});
  await tick(); assert.equal(h.requests.length,0); assert.equal(h.run('recordingAction'),'copyName');
  add.events.click({detail:1,altKey:true,shiftKey:true}); await tick();
  assert.deepEqual(h.take('/api/settings').body.bindings,{copyName:['CmdOrCtrl+Shift+C','Alt+Shift+Click']});
});

test('a plain click on the recorder stops it instead of taking clicks away from the rows', async()=>{
  const h=await settingsOpen(); const add=h.run('addButtons.get("terminal")');
  add.events.click({detail:1}); assert.equal(h.run('recordingAction'),'terminal');
  add.events.click({detail:1}); assert.equal(h.run('recordingAction'),null);
  await tick(); assert.equal(h.requests.length,0);
  const fixed=h.elements.get('bindings').children[0].children[1].children[0];
  assert.equal(fixed.children[0].textContent,'Click'); assert.equal(fixed.children.length,1);
});

test('a click elsewhere stops recording', async()=>{
  const h=await settingsOpen(); h.run('startBinding("clear")');
  h.documentEvents.click({target:h.elements.get('themeSelect')});
  assert.equal(h.run('recordingAction'),null);
});

test('removing a binding unbinds it, and restoring the defaults sends an empty set', async()=>{
  const h=await settingsOpen();
  const firefoxRow=h.elements.get('bindings').children[2], chips=firefoxRow.children[1].children;
  assert.deepEqual(chips.slice(0,2).map(chip=>chip.children[0].textContent),['⌘Click','⌘F']);
  chips[0].children[1].events.click(); await tick();
  const save=h.take('/api/settings'); assert.deepEqual(save.body.bindings,{firefox:['CmdOrCtrl+F']});
  save.resolve(save.body); await tick();
  assert.equal(h.run('actionFor("CmdOrCtrl+Click")'),undefined);
  h.elements.get('bindingsReset').events.click(); await tick();
  const reset=h.take('/api/settings'); assert.deepEqual(reset.body.bindings,{});
  reset.resolve(reset.body); await tick();
  assert.equal(h.run('actionFor("CmdOrCtrl+Click").id'),'firefox'); assert.equal(h.elements.get('bindingsReset').hidden,true);
});

test('an unbound modified click does nothing, while a plain click still switches', async()=>{
  const h=await panel({profile:'A',profiles:[{name:'A'},{name:'B'}],bindings:{switch:[]}});
  h.run('rows[1].element').events.click({shiftKey:true,detail:1});
  assert.equal(h.requests.length,0);
  h.run('rows[1].element').events.click({detail:1});
  assert.equal(h.take('/api/profile/set').body.name,'B');
});

test('a preference saved before the settings were read leaves the bindings alone', async()=>{
  const h=await panel(); const saved=h.run('saveSettings({theme:"dark"})'); await tick();
  const write=h.take('/api/settings'); assert.equal('bindings' in write.body,false);
  write.resolve(write.body); await saved;
});

// --- columns ----------------------------------------------------------------

test('the account name is read back out of the name awsm sso update gave the profile', async()=>{
  const h=await panel();
  const name=profile=>h.run(`accountName(${JSON.stringify(profile)})`);
  assert.equal(name({name:'besharp-besharp-isotopes-administratoraccess',sso_session:'besharp',sso_role_name:'AdministratorAccess'}),'besharp-isotopes');
  // Sessions and roles are cleaned the way awsm cleans them.
  assert.equal(name({name:'acme-co-prod-awsreadonlyaccess',sso_session:'Acme_Co',sso_role_name:'AWSReadOnlyAccess'}),'prod');
  // A collision suffix comes off first, with or without its counter.
  for (const suffix of ['-123456789012','-123456789012-2']) {
    assert.equal(name({name:'team-shared-admins'+suffix,sso_session:'team',sso_role_name:'Admins',sso_account_id:'123456789012'}),'shared');
  }
  // Anything not named that way has no account name to recover.
  for (const profile of [
    {name:'my-own-name',sso_session:'team',sso_role_name:'Admins'},
    {name:'team-admins',sso_session:'team',sso_role_name:'Admins'},
    {name:'static-keys'},
    {name:'team-prod-admins',sso_session:'team'},
  ]) assert.equal(name(profile),'',JSON.stringify(profile));
});

test('every row shows session, account, permission set and region, in that order', async()=>{
  const h=await panel({profile:'acme-prod-admins',profiles:[
    {name:'acme-prod-admins',sso_session:'acme',sso_role_name:'Admins',region:'eu-west-1'},
    {name:'legacy-keys',region:'us-east-1',mfa_serial:'arn:aws:iam::1:mfa/me'},
  ]});
  const cells=i=>h.run(`rows[${i}].element.children`).map(c=>[c.className,c.textContent]);
  assert.deepEqual(cells(0),[['cell row-session','acme'],['cell row-name','prodActive'],['cell row-role','Admins'],['cell row-region','eu-west-1']]);
  // Without an account to read, the profile's own name stands in for it.
  assert.deepEqual(cells(1),[['cell row-session',''],['cell row-name','legacy-keysMFA'],['cell row-role',''],['cell row-region','us-east-1']]);
  assert.equal(h.run('rows[0].element.title').startsWith('acme-prod-admins'),true);
  assert.equal(h.run('rows[0].element.attributes["aria-label"]'),'prod · Active · acme · Admins · eu-west-1');
  const headings=h.elements.get('list').children[0];
  assert.equal(headings.className,'columns');
  assert.deepEqual(headings.children.map(c=>c.textContent),['SSO session','Account','Permission set','Region']);
});

test('no column headings over an empty list', async()=>{
  const h=await panel(); search(h,'nothing matches this');
  assert.deepEqual(h.elements.get('list').children.map(c=>c.className),['empty']);
});

test('a CamelCase permission set may wrap between its words, and only there', async()=>{
  const h=await panel();
  const breaks=text=>h.run(`wordBreaks(${JSON.stringify(text)})`).split('​');
  assert.deepEqual(breaks('RepositoriesAndPipelinesAccess'),['Repositories','And','Pipelines','Access']);
  assert.deepEqual(breaks('KleecksMAPAssumeAdminRole'),['Kleecks','MAP','Assume','Admin','Role']);
  assert.deepEqual(breaks('AWSReadOnlyAccess'),['AWS','Read','Only','Access']);
  assert.deepEqual(breaks('aws-laspo-mes-ops-developer'),['aws-laspo-mes-ops-developer']);
});

// --- fitting the window -----------------------------------------------------

// At seven points a character, with the paddings the harness reports:
//   session      longest is "longer-session-name"          19 → 133
//   account      "shared-services" + the Active badge kept  21 → 147
//   permission   the heading, "Permission set", is longest  14 →  98
//   region       "ap-southeast-2"                           14 →  98
//   list   133 + 147 + 98 + 98 + 3 gaps of 10 + 16 + 16    = 538
//   pane   "longer-session-name" + its count "1"   20 → 140 + 8 + 6 + 1 = 155
const fitState = {profile:'static', profiles:[
  {name:'team-shared-services-admins',sso_session:'team',sso_role_name:'Admins',region:'eu-west-1'},
  {name:'longer-session-name-x-readonly',sso_session:'longer-session-name',sso_role_name:'ReadOnly',region:'ap-southeast-2'},
  {name:'static'},
]};
const rootStyle = (h, name) => h.run(`document.documentElement.style[${JSON.stringify(name)}]`);

test('the window is asked to be as wide as the longest session, account, permission set and region', async()=>{
  const h=await panel(fitState);
  assert.deepEqual(h.resizes,[155+538]);
  assert.equal(rootStyle(h,'--profile-columns'),'minmax(133px, 133fr) minmax(147px, 147fr) minmax(98px, 98fr) minmax(98px, 98fr)');
  assert.equal(rootStyle(h,'--sessions-width'),'155px');
});

test('fitted once per set of profiles: reopening, searching, filtering and switching leave the width alone', async()=>{
  const h=await panel(fitState);
  const reload=async next=>{ const done=h.run('load()'); h.take('/api/state').resolve(next); await done; await tick(); };
  await reload(fitState);
  search(h,'shared'); chooseSession(h,'team'); search(h,'');
  await reload({...fitState, profile:'team-shared-services-admins'});
  assert.deepEqual(h.resizes,[693]);
  // A longer account is a reason to measure again.
  await reload({...fitState, profiles:[...fitState.profiles,{name:'team-a-much-longer-account-name-admins',sso_session:'team',sso_role_name:'Admins'}]});
  assert.equal(h.resizes.length,2);
  assert.equal(h.resizes[1],693+('a-much-longer-account-nameActive'.length-21)*7);
});

test('a screen too narrow for the names shares out what there is, and only then do they wrap', async()=>{
  const h=await panel(fitState,new Map(),{grant:()=>500});
  await tick();
  assert.equal(rootStyle(h,'--profile-columns'),'minmax(0px, 133fr) minmax(0px, 147fr) minmax(0px, 98fr) minmax(0px, 98fr)');
  assert.equal(rootStyle(h,'--sessions-width'),'125px');
});

test('a resize that fails is asked for again on the next load', async()=>{
  let fail=true;
  const h=await panel(fitState,new Map(),{grant:width=>{ if (fail) throw new Error('no window'); return width; }});
  await tick(); fail=false;
  const done=h.run('load()'); h.take('/api/state').resolve(fitState); await done; await tick();
  assert.deepEqual(h.resizes,[693,693]);
});
