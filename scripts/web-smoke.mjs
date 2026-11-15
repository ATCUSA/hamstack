import vm from 'node:vm';
class Element {
 constructor(tag='div'){this.tagName=tag;this.children=[];this.value='';this.hidden=false;this.textContent=''}
 append(...nodes){this.children.push(...nodes)}
 replaceChildren(...nodes){this.children=[...nodes]}
 setAttribute(){}
 scrollIntoView(){}
}
const nodes=new Map();const listeners=new Map();
const document={hidden:false,createElement:tag=>new Element(tag),getElementById:id=>{if(!nodes.has(id))nodes.set(id,new Element());return nodes.get(id)},addEventListener:(event,cb)=>listeners.set(event,cb)};
const url=process.argv[2];const html=await(await fetch(url)).text();
const context=vm.createContext({document,window:{confirm:()=>false},location:{hostname:'127.0.0.1',href:url},fetch:(path,options)=>fetch(new URL(path,url),options),setInterval:()=>0,console,JSON,encodeURIComponent});
for(const match of html.matchAll(/<script>([\s\S]*?)<\/script>/g))vm.runInContext(match[1],context);
listeners.get('DOMContentLoaded')();
await new Promise(resolve=>setTimeout(resolve,200));
const nav=nodes.get('manage-nav');if(!nav?.children.length)throw Error('management navigation did not render');
const names=nav.children.map(x=>x.textContent);
for(const name of names){const button=nodes.get('manage-nav').children.find(x=>x.textContent===name);button.onclick();if(!nodes.get('manage-panel').children.length)throw Error(name+' rendered an empty panel');console.log('Rendered '+name)}
if(!nodes.get('services').children.length)throw Error('live services did not render');
console.log('Live services and all management panels rendered using the server preview API.');
