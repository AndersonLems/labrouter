// Structured, human-readable views for raw system data (ip -j, ping, traceroute,
// resolv.conf, iw station dump, nft list). Every view degrades to a readable
// fallback when the input does not match the expected format.
import React,{useState} from 'react';
import {Activity,AlertTriangle,ArrowDownLeft,ArrowUpRight,Check,Clock,Globe,Lock,Network,Plus,Radio,Route,Search,Server,Shield,Trash2,Users,Wifi,X} from 'lucide-react';

type Data=Record<string,any>;

/* ---------- formatting helpers ---------- */
export const fmtBytes=(n:number)=>!Number.isFinite(n)?'—':n>=1e9?(n/1e9).toFixed(1)+' GB':n>=1e6?(n/1e6).toFixed(1)+' MB':n>=1e3?(n/1e3).toFixed(0)+' KB':n+' B';
// Network throughput in bits per second (input: bytes per second), as speedtests report it.
export const fmtBits=(bytesPerSec:number)=>{const b=bytesPerSec*8;return !Number.isFinite(b)?'—':b>=1e9?(b/1e9).toFixed(2)+' Gbps':b>=1e6?(b/1e6).toFixed(b>=1e8?0:1)+' Mbps':b>=1e3?(b/1e3).toFixed(0)+' Kbps':b.toFixed(0)+' bps'};
export const fmtTime=(v:any)=>!v||String(v).startsWith('0001')?'Ainda não registrado':new Date(v).toLocaleString('pt-BR');
const fmtNum=(n:number)=>Number.isFinite(n)?n.toLocaleString('pt-BR'):'—';
const fmtMs=(v:number)=>(v<10?v.toFixed(2):v<100?v.toFixed(1):v.toFixed(0))+' ms';
export function fmtDuration(sec:number){if(!Number.isFinite(sec)||sec<0)return '—';const d=Math.floor(sec/86400),h=Math.floor(sec%86400/3600),m=Math.floor(sec%3600/60),s=Math.floor(sec%60);return d?`${d}d ${h}h`:h?`${h}h ${String(m).padStart(2,'0')}m`:m?`${m}m ${String(s).padStart(2,'0')}s`:`${s}s`}
const latencyClass=(v:number)=>v<60?'good':v<150?'mid':'bad';

function ipKind(ip:string):{label:string,cls:string}{
 if(ip.includes(':'))return ip.startsWith('fe80')?{label:'Link-local',cls:'neutral'}:ip==='::1'?{label:'Loopback',cls:'neutral'}:{label:'IPv6',cls:'info'};
 const p=ip.split('.').map(Number);if(p.length!==4||p.some(isNaN))return {label:'',cls:''};
 if(p[0]===10||(p[0]===172&&p[1]>=16&&p[1]<=31)||(p[0]===192&&p[1]===168))return {label:'Privado',cls:'neutral'};
 if(p[0]===100&&p[1]>=64&&p[1]<=127)return {label:'CGNAT',cls:'warn'};
 if(p[0]===127)return {label:'Loopback',cls:'neutral'};
 if(p[0]===169&&p[1]===254)return {label:'Link-local',cls:'neutral'};
 return {label:'Público',cls:'info'};
}

/* ---------- small building blocks ---------- */
export function Pill({state,label}:{state:boolean|undefined,label:string}){return <span className={'pill '+(state?'good':state===false?'warn':'neutral')}><i/>{label}</span>}
export function Tag({cls,children,title}:{cls?:string,children:React.ReactNode,title?:string}){return <span className={'tag '+(cls||'')} title={title}>{children}</span>}
export function Empty({children}:{children:React.ReactNode}){return <div className="empty"><Radio size={25}/><p>{children}</p></div>}
export function Raw({value,label='Ver saída original'}:{value:any,label?:string}){if(value===undefined||value===null||value==='')return null;return <details className="raw"><summary>{label}</summary><pre className="code">{typeof value==='string'?value:JSON.stringify(value,null,2)}</pre></details>}
export function KV({items}:{items:[string,React.ReactNode][]}){return <div className="kv-grid">{items.map(([k,v])=><div key={k}><span>{k}</span><strong>{v===undefined||v===null||v===''?'—':v}</strong></div>)}</div>}
function Stat({label,value,detail,icon:Icon,tone}:{label:string,value:React.ReactNode,detail?:React.ReactNode,icon?:typeof Globe,tone?:'good'|'warn'|'bad'}){return <div className={'stat '+(tone||'')}><div className="stat-top"><span>{label}</span>{Icon&&<Icon size={15}/>}</div><strong>{value}</strong>{detail&&<small>{detail}</small>}</div>}
function Panel({title,icon:Icon,actions,children,className}:{title:string,icon:typeof Globe,actions?:React.ReactNode,children:React.ReactNode,className?:string}){return <section className={'panel '+(className||'')}><div className="panel-heading"><div><Icon size={18}/><h2>{title}</h2></div>{actions}</div><div className="panel-body">{children}</div></section>}

export type Column={key:string,label:string,render?:(row:Data)=>React.ReactNode};
export function Table({rows,columns,search=true}:{rows:Data[],columns:Column[],search?:boolean}){
 const [q,setQ]=useState('');const list=Array.isArray(rows)?rows:[];
 const items=q?list.filter(x=>JSON.stringify(x).toLowerCase().includes(q.toLowerCase())):list;
 return <>{search&&<div className="table-tools"><Search size={16}/><input placeholder="Buscar nos resultados…" value={q} onChange={e=>setQ(e.target.value)}/><span>{items.length} {items.length===1?'registro':'registros'}</span></div>}<div className="table-scroll"><table><thead><tr>{columns.map(c=><th key={c.key}>{c.label}</th>)}</tr></thead><tbody>{items.map((r,i)=><tr key={r.id??i}>{columns.map(c=><td key={c.key}>{c.render?c.render(r):typeof r[c.key]==='boolean'?(r[c.key]?'Sim':'Não'):String(r[c.key]??'—')}</td>)}</tr>)}</tbody></table></div>{!items.length&&<Empty>Nenhum registro disponível.</Empty>}</>;
}
export const timeColumn=(label='Data / hora'):Column=>({key:'time',label,render:r=>fmtTime(r.time)});
export const resultColumn:Column={key:'result',label:'Resultado',render:r=>{const v=String(r.result??'');const bad=/erro|fail|fault|falh|denied|negad|reject|invál/i.test(v),ok=/^(ok|success|sucesso|aplicad|accepted|aceit)/i.test(v);return <span className={'result '+(bad?'bad':ok?'good':'')}>{v||'—'}</span>}};

/* ---------- interfaces ---------- */
const ifaceRole=(n:string)=>n==='wlp2s0'?'WAN · Wi-Fi principal':n==='ap-tr069'?'LAN do laboratório':n==='lo'?'Loopback':/^(tun|wg|tap)/.test(n)?'Túnel / VPN':/^(lxc|docker|br|virbr|veth)/.test(n)?'Interface virtual':/^(wl|wlan)/.test(n)?'Wi-Fi':/^(en|eth)/.test(n)?'Ethernet':'Interface';
const ifaceUp=(i:Data)=>i.operstate==='UP'||(i.operstate==='UNKNOWN'&&(i.flags||[]).includes('UP'));
const ifaceIcon=(n:string)=>n==='lo'?Server:/^(tun|wg|tap)/.test(n)?Lock:/^(wl|ap-)/.test(n)?Wifi:Network;
const addrs=(i:Data)=>(Array.isArray(i.addr_info)?i.addr_info:[]) as Data[];
export const ipv4Of=(i?:Data)=>i?addrs(i).find(a=>a.family==='inet')?.local:undefined;
const lifetime=(s:number)=>s===undefined?'—':s>=4294967295?'Permanente':fmtDuration(s);

export function InterfaceCard({i}:{i:Data}){
 const up=ifaceUp(i),Icon=ifaceIcon(i.ifname),st=i.stats64||i.stats||{},rx=st.rx||{},tx=st.tx||{},errs=(rx.errors||0)+(tx.errors||0),drops=(rx.dropped||0)+(tx.dropped||0);
 return <div className={'iface-card '+(up?'':'down')}>
  <div className="iface-head"><div className={'iface-icon '+(up?'on':'')}><Icon size={18}/></div><div><strong>{i.ifname}</strong><small>{ifaceRole(i.ifname)}</small></div><Pill state={up} label={up?'Ativa':'Inativa'}/></div>
  <div className="iface-addrs">{addrs(i).length?addrs(i).map((a,k)=><div key={k}><Tag cls={a.family==='inet'?'info':''}>{a.family==='inet'?'IPv4':'IPv6'}</Tag><code>{a.local}/{a.prefixlen}</code>{a.dynamic&&<Tag>DHCP</Tag>}{a.scope&&a.scope!=='global'&&<small>{a.scope==='link'?'link-local':a.scope}</small>}</div>):<span className="muted">Sem endereço IP</span>}</div>
  <KV items={[['MAC',i.link_type==='none'||!i.address?'—':<code>{i.address}</code>],['MTU',i.mtu],['Recebido',<>{fmtBytes(rx.bytes||0)} <small className="muted">· {fmtNum(rx.packets||0)} pct</small></>],['Enviado',<>{fmtBytes(tx.bytes||0)} <small className="muted">· {fmtNum(tx.packets||0)} pct</small></>]]}/>
  {(errs>0||drops>0)&&<div className="iface-warn"><AlertTriangle size={13}/>{fmtNum(errs)} erros · {fmtNum(drops)} pacotes descartados</div>}
 </div>;
}
function sortIfaces(list:Data[]){const rank=(i:Data)=>i.ifname==='wlp2s0'?0:i.ifname==='ap-tr069'?1:i.ifname==='lo'?4:ifaceUp(i)?2:3;return [...list].sort((a,b)=>rank(a)-rank(b)||String(a.ifname).localeCompare(b.ifname))}
function Unavailable({value}:{value:any}){return <Empty>{value?.reason||'Dados indisponíveis no momento.'}</Empty>}

export function InterfacesView({value,title='Interfaces de rede'}:{value:any,title?:string}){
 const [all,setAll]=useState(false),[q,setQ]=useState('');
 if(!Array.isArray(value))return <Panel title={title} icon={Network}><Unavailable value={value}/></Panel>;
 const hidden=value.filter(i=>!ifaceUp(i)).length;
 const list=sortIfaces(value).filter(i=>(all||ifaceUp(i))&&(!q||JSON.stringify(i).toLowerCase().includes(q.toLowerCase())));
 return <Panel title={title} icon={Network} actions={<span className="subtle">{value.length} interfaces · {value.length-hidden} ativas</span>}>
  <div className="toolbar"><div className="search"><Search size={15}/><input placeholder="Filtrar por nome, IP ou MAC…" value={q} onChange={e=>setQ(e.target.value)}/></div>{hidden>0&&<label className="check"><input type="checkbox" checked={all} onChange={e=>setAll(e.target.checked)}/>Mostrar inativas ({hidden})</label>}</div>
  <div className="iface-grid">{list.map(i=><InterfaceCard key={i.ifindex??i.ifname} i={i}/>)}</div>{!list.length&&<Empty>Nenhuma interface corresponde ao filtro.</Empty>}
 </Panel>;
}

export function AddressesView({value}:{value:any}){
 if(!Array.isArray(value))return <Panel title="Endereços IP" icon={Globe}><Unavailable value={value}/></Panel>;
 const rows=sortIfaces(value).flatMap(i=>addrs(i).map(a=>({...a,ifname:i.ifname,up:ifaceUp(i)})));
 return <section className="panel"><div className="panel-heading"><div><Globe size={18}/><h2>Endereços IP</h2></div><span className="subtle">Atribuídos a todas as interfaces</span></div>
  <Table rows={rows} columns={[{key:'ifname',label:'Interface'},{key:'local',label:'Endereço',render:r=><span className="addr"><code>{r.local}/{r.prefixlen}</code>{(()=>{const k=ipKind(r.local);return k.label&&<Tag cls={k.cls}>{k.label}</Tag>})()}</span>},{key:'family',label:'Família',render:r=>r.family==='inet'?'IPv4':'IPv6'},{key:'scope',label:'Escopo',render:r=>({global:'Global',link:'Link-local',host:'Somente host'} as Data)[r.scope]||r.scope||'—'},{key:'dynamic',label:'Origem',render:r=>r.dynamic?'Dinâmico (DHCP/SLAAC)':'Estático'},{key:'valid_life_time',label:'Validade',render:r=>lifetime(r.valid_life_time)},{key:'up',label:'Interface',render:r=><Pill state={r.up} label={r.up?'Ativa':'Inativa'}/>}]}/>
 </section>;
}

/* ---------- routes ---------- */
const proto=(p:string)=>({kernel:'Conectada (kernel)',dhcp:'DHCP',static:'Estática',boot:'Boot',ra:'Router Advertisement',redirect:'Redirect'} as Data)[p]||p||'—';
export function RoutesView({value}:{value:any}){
 const [scope,setScope]=useState<'main'|'all'>('main');
 if(!Array.isArray(value))return <Panel title="Tabela de rotas" icon={Route}><Unavailable value={value}/></Panel>;
 const main=value.filter((r:Data)=>!r.table||r.table==='main');
 const rows=(scope==='main'?main:value).map((r:Data,i:number)=>({...r,id:i}));
 const defaults=main.filter((r:Data)=>r.dst==='default');
 return <>
  <div className="stat-grid">{defaults.length?defaults.map((r:Data,i:number)=><Stat key={i} icon={Route} label={'ROTA PADRÃO'+(defaults.length>1?` #${i+1}`:'')} value={r.gateway||'Direta'} detail={<>via {r.dev}{r.metric!==undefined&&<> · métrica {r.metric}</>}{r.prefsrc&&<> · origem {r.prefsrc}</>}</>} tone="good"/>):<Stat icon={Route} label="ROTA PADRÃO" value="Ausente" detail="Sem saída para a internet" tone="bad"/>}<Stat label="ROTAS NA TABELA PRINCIPAL" value={main.length} detail={`${value.length} considerando todas as tabelas`} icon={Network}/></div>
  <section className="panel"><div className="panel-heading"><div><Route size={18}/><h2>Tabela de rotas</h2></div><div className="segmented small"><button className={scope==='main'?'on':''} onClick={()=>setScope('main')}>Principal</button><button className={scope==='all'?'on':''} onClick={()=>setScope('all')}>Todas as tabelas</button></div></div>
  <Table rows={rows} columns={[
   {key:'dst',label:'Destino',render:r=>r.dst==='default'?<Tag cls="info">Padrão · 0.0.0.0/0</Tag>:<code>{r.type&&r.type!=='unicast'?r.type+' ':''}{r.dst}</code>},
   {key:'gateway',label:'Próximo salto',render:r=>r.gateway?<code>{r.gateway}</code>:<span className="muted">Rede conectada</span>},
   {key:'dev',label:'Interface'},
   {key:'prefsrc',label:'IP de origem',render:r=>r.prefsrc||'—'},
   {key:'protocol',label:'Origem da rota',render:r=>proto(r.protocol)},
   {key:'metric',label:'Métrica',render:r=>r.metric??'—'},
   ...(scope==='all'?[{key:'table',label:'Tabela',render:(r:Data)=>r.table||'main'}]:[]),
   {key:'flags',label:'Estado',render:r=>(r.flags||[]).includes('linkdown')?<Pill state={false} label="Link inativo"/>:<Pill state={true} label="Ativa"/>}]}/></section></>;
}

/* ---------- DNS (resolv.conf) ---------- */
export function DnsView({value}:{value:any}){
 const text=typeof value==='string'?value:'';
 const lines=text.split('\n').map(l=>l.trim()).filter(l=>l&&!l.startsWith('#')&&!l.startsWith(';'));
 const servers=lines.filter(l=>l.startsWith('nameserver')).map(l=>l.split(/\s+/)[1]);
 const search=lines.filter(l=>/^(search|domain)\s/.test(l)).flatMap(l=>l.split(/\s+/).slice(1));
 const options=lines.filter(l=>l.startsWith('options')).flatMap(l=>l.split(/\s+/).slice(1));
 const stub=servers.some(s=>s.startsWith('127.0.0.5'));
 return <Panel title="Resolução de nomes (DNS)" icon={Globe} actions={<span className="subtle">/etc/resolv.conf</span>}>
  {stub&&<div className="notice"><AlertTriangle size={17}/><div><strong>Resolvedor local do systemd-resolved</strong><span>O sistema consulta 127.0.0.53, que encaminha para os servidores DNS recebidos da rede. Use <code>resolvectl status</code> no host para ver os servidores upstream.</span></div></div>}
  <div className="dns-list">{servers.length?servers.map((s,i)=><div className="dns-row" key={s+i}><span className="dns-index">{i+1}</span><code>{s}</code>{(()=>{const k=ipKind(s);return k.label&&<Tag cls={k.cls}>{k.label}</Tag>})()}<small>{i===0?'Servidor primário':'Servidor alternativo'}</small></div>):<Empty>Nenhum servidor DNS configurado.</Empty>}</div>
  <KV items={[['Domínios de busca',search.join(', ')||'Nenhum'],['Opções',options.join(' · ')||'Padrão do sistema']]}/>
  <Raw value={text} label="Ver arquivo completo"/>
 </Panel>;
}

/* ---------- WAN overview ---------- */
export function WanOverview({data}:{data:Data}){
 const w=data.wireless||{},ifs=Array.isArray(data.interfaces)?data.interfaces:[],i=ifs.find((x:Data)=>x.ifname===(w.interface||'wlp2s0')),routes=Array.isArray(data.routes)?data.routes:[];
 const def=routes.find((r:Data)=>r.dst==='default'&&r.dev===(w.interface||'wlp2s0')&&(!r.table||r.table==='main'));
 const dns=(typeof data.dns==='string'?data.dns:'').split('\n').filter(l=>l.trim().startsWith('nameserver')).map(l=>l.trim().split(/\s+/)[1]);
 const tx=/txpower ([\d.]+) dBm/.exec(w.info||'')?.[1],type=/type (\w+)/.exec(w.info||'')?.[1];
 const st=i?.stats64||i?.stats||{},connected=Boolean(w.ssid),ip=ipv4Of(i),ipv4=i?addrs(i).find(a=>a.family==='inet'):undefined;
 return <>
  <div className="stat-grid four">
   <Stat icon={Wifi} label="REDE CONECTADA" value={w.ssid||'Desconectada'} detail={connected?`${w.frequency>=5000?'5 GHz':'2.4 GHz'} · canal ${w.channel}`:'Sem associação Wi-Fi'} tone={connected?'good':'bad'}/>
   <Stat icon={Globe} label="ENDEREÇO IPv4" value={ip||'Sem endereço'} detail={ipv4?`/${ipv4.prefixlen} · ${ipv4.dynamic?'via DHCP':'estático'}`:undefined} tone={ip?undefined:'warn'}/>
   <Stat icon={Route} label="GATEWAY PADRÃO" value={def?.gateway||'—'} detail={def?`métrica ${def.metric??0} · ${proto(def.protocol)}`:'Nenhuma rota padrão nesta interface'}/>
   <Stat icon={Server} label="DNS" value={dns[0]||'—'} detail={dns.length>1?`+${dns.length-1} servidor(es)`:'Servidor configurado'}/>
  </div>
  <section className="panel"><div className="panel-heading"><div><Wifi size={18}/><h2>Conexão Wi-Fi da WAN</h2></div><Pill state={connected} label={connected?'Associada':'Sem associação'}/></div><div className="panel-body">
   <KV items={[['Interface',w.interface],['Rádio físico',w.phy],['SSID',w.ssid],['Canal',w.channel||'—'],['Frequência',w.frequency?`${w.frequency} MHz`:'—'],['Modo',type==='managed'?'Cliente (managed)':type],['Potência de transmissão',tx?`${tx} dBm`:'—'],['MAC',i?.address?<code>{i.address}</code>:'—'],['MTU',i?.mtu],['Estado do link',i?(ifaceUp(i)?'Ativo':'Inativo'):'—'],['Total recebido',fmtBytes(st.rx?.bytes||0)],['Total enviado',fmtBytes(st.tx?.bytes||0)]]}/>
   <Raw value={w.info} label="Ver saída do iw"/>
  </div></section>
 </>;
}

/* ---------- ping ---------- */
type PingReply={seq:number,ttl?:number,time?:number,error?:string,from?:string};
export function parsePing(text:string){
 const replies:PingReply[]=[];let host='',ip='',sent=0,received=0,loss=NaN,min=NaN,avg=NaN,max=NaN,mdev=NaN;
 for(const l of text.split('\n')){let m;
  if((m=/^PING (\S+) \(([^)]+)\)/.exec(l))){host=m[1];ip=m[2]}
  else if((m=/from ([^\s:]+).*icmp_seq=(\d+)\s+ttl=(\d+)\s+time=([\d.]+)\s*ms/.exec(l)))replies.push({from:m[1],seq:+m[2],ttl:+m[3],time:+m[4]});
  else if((m=/^From (\S+) icmp_seq=(\d+) (.+)$/.exec(l)))replies.push({from:m[1],seq:+m[2],error:m[3]});
  else if((m=/(\d+) packets transmitted, (\d+) (?:packets )?received.*?([\d.]+)% packet loss/.exec(l))){sent=+m[1];received=+m[2];loss=+m[3]}
  else if((m=/= ([\d.]+)\/([\d.]+)\/([\d.]+)\/([\d.]+) ms/.exec(l))){min=+m[1];avg=+m[2];max=+m[3];mdev=+m[4]}
 }
 for(let s=1;s<=sent;s++)if(!replies.some(r=>r.seq===s))replies.push({seq:s,error:'Sem resposta (timeout)'});
 replies.sort((a,b)=>a.seq-b.seq);
 return {host,ip,sent,received,loss,min,avg,max,mdev,replies,valid:Boolean(host||sent)};
}
function ToolFailure({text}:{text:string}){
 const perm=/n[ãa]o permitida|not permitted|cap_net_raw|permission denied|permiss[ãa]o negada/i.test(text),missing=/not found|n[ãa]o encontrado|executable file/i.test(text);
 return <><div className="diag-banner bad"><X size={18}/><div><strong>{perm?'O serviço não tem permissão para enviar pacotes ICMP':missing?'Ferramenta não instalada no sistema':'O comando falhou antes de produzir um resultado'}</strong><span>{perm?'O lab-router-api roda sem privilégios e o kernel bloqueia ping sem CAP_NET_RAW (net.ipv4.ping_group_range). Ajuste a unidade systemd ou o sysctl para habilitar este teste.':missing?'Instale o pacote correspondente (ex.: iputils-ping, traceroute) no host.':'Veja a mensagem retornada pelo sistema abaixo.'}</span></div></div><div className="diag-fallback spaced"><pre className="code">{text||'Sem saída.'}</pre></div></>;
}
export function PingResult({text}:{text:string}){
 const p=parsePing(text);if(!p.valid)return <ToolFailure text={text}/>;
 const ok=p.received>0,top=Math.max(...p.replies.map(r=>r.time||0),1);
 return <>
  <div className={'diag-banner '+(p.loss===0?'good':ok?'mid':'bad')}>{p.loss===0?<Check size={18}/>:ok?<AlertTriangle size={18}/>:<X size={18}/>}<div><strong>{p.loss===0?`${p.host} respondeu a todos os pacotes`:ok?`${p.host} respondeu parcialmente`:`${p.host} não respondeu`}</strong><span>{p.host!==p.ip&&p.ip?`Resolvido para ${p.ip} · `:''}{p.sent} pacotes ICMP enviados</span></div></div>
  <div className="stat-grid four">
   <Stat label="PACOTES" value={`${p.received}/${p.sent}`} detail="recebidos / enviados" icon={Activity}/>
   <Stat label="PERDA" value={`${Number.isFinite(p.loss)?+p.loss.toFixed(1):'—'}%`} tone={p.loss===0?'good':p.loss<100?'warn':'bad'} detail={p.loss===0?'Nenhum pacote perdido':'Pacotes sem resposta'} icon={AlertTriangle}/>
   <Stat label="LATÊNCIA MÉDIA" value={Number.isFinite(p.avg)?fmtMs(p.avg):'—'} tone={Number.isFinite(p.avg)?(latencyClass(p.avg)==='good'?'good':latencyClass(p.avg)==='mid'?'warn':'bad'):undefined} detail={Number.isFinite(p.min)?`mín ${fmtMs(p.min)} · máx ${fmtMs(p.max)}`:'Sem medição'} icon={Clock}/>
   <Stat label="VARIAÇÃO (JITTER)" value={Number.isFinite(p.mdev)?fmtMs(p.mdev):'—'} detail="Desvio médio do RTT" icon={Activity}/>
  </div>
  <div className="probe-list">{p.replies.map(r=><div className="probe-row" key={r.seq}><span className="probe-seq">#{r.seq}</span>{r.time!==undefined?<><div className="bar"><i className={latencyClass(r.time)} style={{width:Math.max(3,r.time/top*100)+'%'}}/></div><strong className={'lat '+latencyClass(r.time)}>{fmtMs(r.time)}</strong><small>TTL {r.ttl}</small></>:<><div className="bar"/><strong className="lat bad">—</strong><small className="err">{r.error}</small></>}</div>)}</div>
  <Raw value={text}/>
 </>;
}

/* ---------- traceroute ---------- */
type Probe={ip?:string,rtt?:number,note?:string};
export function parseTraceroute(text:string){
 let target='',ip='',maxHops=0;const hops:{n:number,probes:Probe[]}[]=[];
 for(const l of text.split('\n')){let m;
  if((m=/^traceroute to (\S+) \(([^)]+)\), (\d+) hops max/.exec(l))){target=m[1];ip=m[2];maxHops=+m[3];continue}
  if(!(m=/^\s*(\d+)\s+(.*)$/.exec(l)))continue;
  const tokens=m[2].trim().split(/\s+/),probes:Probe[]=[];let cur:string|undefined;
  for(let i=0;i<tokens.length;i++){const t=tokens[i];
   if(t==='*')probes.push({});
   else if(/^[\d.]+$/.test(t)&&tokens[i+1]==='ms'){probes.push({ip:cur,rtt:+t});i++}
   else if(/^!/.test(t)){if(probes.length)probes[probes.length-1].note=t}
   else if(t!=='ms'){cur=t.replace(/[()]/g,'')}
  }
  hops.push({n:+m[1],probes});
 }
 return {target,ip,maxHops,hops,valid:hops.length>0};
}
const icmpNote=(n?:string)=>n?({'!H':'Host inalcançável','!N':'Rede inalcançável','!P':'Protocolo inalcançável','!X':'Bloqueado administrativamente','!F':'Fragmentação necessária'} as Data)[n]||n:'';
export function TracerouteResult({text}:{text:string}){
 const t=parseTraceroute(text);if(!t.valid)return <ToolFailure text={text}/>;
 const avg=(ps:Probe[])=>{const v=ps.filter(p=>p.rtt!==undefined).map(p=>p.rtt!);return v.length?v.reduce((a,b)=>a+b,0)/v.length:undefined};
 const last=t.hops[t.hops.length-1],reached=Boolean(last?.probes.some(p=>p.ip===t.ip)),top=Math.max(...t.hops.map(h=>avg(h.probes)||0),1),silent=t.hops.filter(h=>h.probes.every(p=>p.rtt===undefined)).length,final=reached?avg(last.probes):undefined;
 return <>
  <div className={'diag-banner '+(reached?'good':'bad')}>{reached?<Check size={18}/>:<AlertTriangle size={18}/>}<div><strong>{reached?`Destino ${t.target} alcançado em ${last.n} saltos`:`Destino ${t.target} não alcançado`}</strong><span>{t.target!==t.ip?`Resolvido para ${t.ip} · `:''}{reached?'A rota até o destino está completa.':`Rastreio encerrado após ${t.hops.length} saltos (máximo ${t.maxHops}).`}</span></div></div>
  <div className="stat-grid four">
   <Stat label="SALTOS" value={last?.n??'—'} detail={`limite de ${t.maxHops}`} icon={Route}/>
   <Stat label="LATÊNCIA ATÉ O DESTINO" value={final!==undefined?fmtMs(final):'—'} tone={final!==undefined?(latencyClass(final)==='good'?'good':latencyClass(final)==='mid'?'warn':'bad'):undefined} detail="Média das 3 sondas no último salto" icon={Clock}/>
   <Stat label="SALTOS SEM RESPOSTA" value={silent} detail={silent?'Roteadores que não respondem às sondas':'Todos responderam'} icon={AlertTriangle}/>
   <Stat label="PRIMEIRO SALTO" value={t.hops[0]?.probes.find(p=>p.ip)?.ip||'—'} detail="Normalmente o gateway local" icon={Network}/>
  </div>
  <div className="hops">{t.hops.map(h=>{const ips=[...new Set(h.probes.map(p=>p.ip).filter(Boolean))] as string[],a=avg(h.probes),silentHop=a===undefined,note=h.probes.find(p=>p.note)?.note;
   return <div className={'hop '+(silentHop?'silent':'')} key={h.n}><span className="hop-n">{h.n}</span>
    <div className="hop-host">{silentHop?<><strong className="muted">Sem resposta</strong><small>O roteador não respondeu às sondas — comum e geralmente inofensivo.</small></>:<>{ips.map(ip=>{const k=ipKind(ip);return <span key={ip} className="addr"><code>{ip}</code>{k.label&&<Tag cls={k.cls}>{k.label}</Tag>}{ip===t.ip&&<Tag cls="good">Destino</Tag>}</span>})}{note&&<small className="err">{icmpNote(note)}</small>}</>}</div>
    <div className="hop-probes">{h.probes.map((p,i)=><span key={i} className={'probe '+(p.rtt===undefined?'lost':latencyClass(p.rtt))}>{p.rtt===undefined?'*':fmtMs(p.rtt)}</span>)}</div>
    <div className="hop-bar"><div className="bar">{a!==undefined&&<i className={latencyClass(a)} style={{width:Math.max(3,a/top*100)+'%'}}/>}</div><strong className={'lat '+(a===undefined?'':latencyClass(a))}>{a!==undefined?fmtMs(a):'—'}</strong></div>
   </div>})}</div>
  <div className="legend"><span><i className="good"/>até 60 ms</span><span><i className="mid"/>60–150 ms</span><span><i className="bad"/>acima de 150 ms</span><span><i className="lost"/>sem resposta</span></div>
  <Raw value={text}/>
 </>;
}

/* ---------- DNS lookup ---------- */
export function DnsResult({value,target}:{value:any,target:string}){
 const list=(Array.isArray(value)?value:[]).map((v:Data)=>typeof v==='string'?v:v.IP+(v.Zone?'%'+v.Zone:''));
 if(!list.length)return <div className="diag-banner bad"><X size={18}/><div><strong>Nenhum endereço encontrado para {target}</strong><span>O nome não existe ou o servidor DNS não respondeu.</span></div></div>;
 const v4=list.filter((x:string)=>!x.includes(':')),v6=list.filter((x:string)=>x.includes(':'));
 return <>
  <div className="diag-banner good"><Check size={18}/><div><strong>{target} resolvido com sucesso</strong><span>{v4.length} endereço(s) IPv4 · {v6.length} endereço(s) IPv6</span></div></div>
  <div className="dns-list">{[...v4,...v6].map((ip:string,i:number)=>{const k=ipKind(ip);return <div className="dns-row" key={ip}><span className="dns-index">{i+1}</span><code>{ip}</code><Tag cls="info">{ip.includes(':')?'AAAA · IPv6':'A · IPv4'}</Tag>{k.label&&k.label!=='IPv6'&&<Tag cls={k.cls}>{k.label}</Tag>}</div>})}</div>
 </>;
}

/* ---------- speed test ---------- */
const mbps=(v:number)=>v>=100?v.toFixed(0):v.toFixed(1);
function Spark({values,cls}:{values:number[],cls:string}){if(!values?.length)return null;const max=Math.max(...values,1),pts=values.map((v,i)=>`${values.length>1?i/(values.length-1)*100:50},${38-v/max*34}`).join(' ');return <svg className={'spark '+cls} viewBox="0 0 100 40" preserveAspectRatio="none"><polygon points={`0,40 ${pts} 100,40`}/><polyline points={pts} vectorEffect="non-scaling-stroke"/></svg>}
const SPEED_PHASES=[{at:0,label:'Medindo latência'},{at:2.5,label:'Medindo download'},{at:11,label:'Medindo upload'},{at:19.5,label:'Finalizando'}];
function SpeedRunning(){const [t,setT]=useState(0);React.useEffect(()=>{const t0=Date.now(),id=setInterval(()=>setT((Date.now()-t0)/1000),200);return()=>clearInterval(id)},[]);const phase=[...SPEED_PHASES].reverse().find(p=>t>=p.at)!;
 return <section className="panel spaced"><div className="panel-body speed-running"><Clock className="spin" size={22}/><div><strong>{phase.label}…</strong><small>{t.toFixed(0)} s de ~20 s · etapa estimada</small></div><div className="speed-steps">{SPEED_PHASES.slice(0,3).map((p,i)=><span key={p.label} className={t>=p.at?(t>=SPEED_PHASES[i+1].at?'done':'on'):''}>{p.label.replace('Medindo ','')}</span>)}</div></div></section>}
export function SpeedResult({r}:{r:Data}){
 const lat=r.latency_ms as number,dl=r.download_mbps as number,ul=r.upload_mbps as number;
 const verdict=dl>=100?'Excelente para streaming 4K, chamadas e downloads grandes':dl>=25?'Boa para streaming HD, videochamadas e uso geral':dl>=10?'Suficiente para navegação e vídeo em qualidade média':'Lenta: pode haver travamentos em vídeo e chamadas';
 const where=[r.city,r.country].filter(Boolean).join(', ');
 return <>
  <div className="speed-hero">
   <div className="speed-big down"><span><ArrowDownLeft size={16}/>DOWNLOAD</span><strong>{mbps(dl)}<small>Mbps</small></strong><Spark values={r.download_samples} cls="down"/><small>{fmtBytes(r.download_bytes)} recebidos</small></div>
   <div className="speed-big up"><span><ArrowUpRight size={16}/>UPLOAD</span><strong>{mbps(ul)}<small>Mbps</small></strong><Spark values={r.upload_samples} cls="up"/><small>{fmtBytes(r.upload_bytes)} enviados</small></div>
  </div>
  <div className="stat-grid four">
   <Stat icon={Clock} label="LATÊNCIA" value={fmtMs(lat)} tone={latencyClass(lat)==='good'?'good':latencyClass(lat)==='mid'?'warn':'bad'} detail="Mediana com a conexão ociosa"/>
   <Stat icon={Activity} label="VARIAÇÃO (JITTER)" value={fmtMs(r.jitter_ms)} tone={r.jitter_ms<30?'good':r.jitter_ms<80?'warn':'bad'} detail={r.latency_failed?`${r.latency_failed} medições sem resposta`:'Diferença média entre medições'}/>
   <Stat icon={Globe} label="IP PÚBLICO" value={r.public_ip||'—'} detail={r.asn?`AS${r.asn}`:'Endereço visto pela internet'}/>
   <Stat icon={Server} label="SERVIDOR" value={r.colo||'Cloudflare'} detail={where||r.server}/>
  </div>
  <div className="diag-banner good"><Check size={18}/><div><strong>{verdict}</strong><span>Medido com {r.streams} conexões paralelas em {Number(r.duration_s).toFixed(0)} s, a partir do host pela WAN (wlp2s0). Inclui qualquer outro tráfego do notebook no momento.</span></div></div>
 </>;
}

/* ---------- network diagnostics page ---------- */
type DiagKind='ping'|'traceroute'|'dns'|'speedtest';
const tools:{id:DiagKind,label:string,desc:string,wait:string}[]=[
 {id:'ping',label:'Ping',desc:'Testa se o destino responde e mede a latência (3 pacotes ICMP).',wait:'Enviando pacotes ICMP…'},
 {id:'traceroute',label:'Traceroute',desc:'Mostra cada roteador no caminho até o destino (até 12 saltos).',wait:'Rastreando a rota — pode levar até 30 segundos…'},
 {id:'dns',label:'DNS Lookup',desc:'Resolve um nome de domínio para endereços IPv4 e IPv6.',wait:'Consultando o DNS…'},
 {id:'speedtest',label:'Speedtest',desc:'Mede download, upload e latência da WAN (~20 s, usa até algumas centenas de MB).',wait:''}];
type Result={kind:DiagKind,target:string,output:any,error?:string,at:Date,ms:number};
export function NetworkDiagnostics({tab,request,notify}:{tab:string,request:(p:string,m?:string,d?:unknown)=>Promise<any>,notify:(m:string)=>void}){
 const [kind,setKind]=useState<DiagKind>(tab==='Traceroute'?'traceroute':tab==='DNS Lookup'?'dns':tab==='Speedtest'?'speedtest':'ping');
 const [target,setTarget]=useState(kind==='dns'?'google.com':'1.1.1.1'),[running,setRunning]=useState(false),[result,setResult]=useState<Result|null>(null);
 const tool=tools.find(t=>t.id===kind)!,speed=kind==='speedtest';
 async function run(e?:React.FormEvent){e?.preventDefault();const tgt=speed?'speed.cloudflare.com':target.trim();if(!tgt)return;setRunning(true);setResult(null);const t0=performance.now();
  try{const r=await request('diagnostics/'+kind,'POST',speed?{}:{target:tgt});setResult({kind,target:tgt,output:speed?r:r.output,at:new Date(),ms:performance.now()-t0})}
  catch(err){const body=(err as any).body;if(speed||(body&&'output' in body))setResult({kind,target:tgt,output:body?.output,error:(err as Error).message,at:new Date(),ms:performance.now()-t0});else notify((err as Error).message)}
  finally{setRunning(false)}}
 return <>
  <section className="panel"><div className="panel-heading"><div><Activity size={18}/><h2>Ferramentas de diagnóstico</h2></div></div><div className="panel-body">
   <div className="tool-tabs">{tools.map(t=><button key={t.id} className={kind===t.id?'on':''} onClick={()=>{setKind(t.id);setResult(null)}} disabled={running}><strong>{t.label}</strong><small>{t.desc}</small></button>)}</div>
   {speed?<div className="diag-form speed-form"><div className="muted">O teste usa o servidor Cloudflare mais próximo (speed.cloudflare.com). Os clientes do laboratório continuam navegando, mas o resultado reflete a banda disponível no momento.</div><button className="primary" disabled={running} onClick={()=>run()}>{running?<Clock size={16} className="spin"/>:<Activity size={16}/>}{running?'Testando…':'Iniciar teste de velocidade'}</button></div>
   :<><form className="diag-form" onSubmit={run}><label className="field">{kind==='dns'?'Nome de domínio':'Destino (IP ou hostname)'}<input value={target} onChange={e=>setTarget(e.target.value)} placeholder={kind==='dns'?'exemplo.com':'1.1.1.1 ou exemplo.com'} spellCheck={false}/></label><button className="primary" disabled={running||!target.trim()}>{running?<Clock size={16} className="spin"/>:<Activity size={16}/>}{running?'Executando…':`Executar ${tool.label.toLowerCase()}`}</button></form>
   <div className="chips">{['1.1.1.1','8.8.8.8','google.com','cloudflare.com'].map(v=><button key={v} type="button" onClick={()=>setTarget(v)}>{v}</button>)}</div></>}
  </div></section>
  {running&&(speed?<SpeedRunning/>:<section className="panel spaced loading"><Clock className="spin"/>{tool.wait}</section>)}
  {!running&&result&&<section className="panel spaced"><div className="panel-heading"><div><Activity size={18}/><h2>Resultado · {tools.find(t=>t.id===result.kind)!.label}{result.kind==='speedtest'?'':` para ${result.target}`}</h2></div><span className="subtle">{result.at.toLocaleTimeString('pt-BR')} · {(result.ms/1000).toFixed(1)} s</span></div><div className="panel-body">
   {result.kind==='speedtest'?(result.error?<div className="diag-banner bad"><X size={18}/><div><strong>Não foi possível concluir o teste</strong><span>{result.error}</span></div></div>:<SpeedResult r={result.output}/>)
   :result.error&&result.kind==='dns'&&!result.output?<div className="diag-banner bad"><X size={18}/><div><strong>Falha ao resolver {result.target}</strong><span>O nome não existe ou o servidor DNS não respondeu.</span></div></div>
   :result.kind==='ping'?<PingResult text={String(result.output??'')}/>:result.kind==='traceroute'?<TracerouteResult text={String(result.output??'')}/>:<DnsResult value={result.output} target={result.target}/>}
  </div></section>}
 </>;
}

/* ---------- Wi-Fi stations ---------- */
type Station={mac:string,iface:string,f:Record<string,string>};
export function parseStations(text:string){const out:Station[]=[];let cur:Station|null=null;for(const l of (text||'').split('\n')){const m=/^Station ([0-9a-fA-F:]+) \(on (\S+)\)/.exec(l);if(m){cur={mac:m[1].toLowerCase(),iface:m[2],f:{}};out.push(cur);continue}const kv=/^\s+([^:]+):\s*(.*)$/.exec(l);if(cur&&kv)cur.f[kv[1].trim()]=kv[2].trim()}return out}
const parseKV=(text:string)=>Object.fromEntries((text||'').split('\n').map(l=>l.split('=')).filter(p=>p.length>=2).map(([k,...v])=>[k.trim(),v.join('=').trim()]));
function Signal({dbm}:{dbm:number}){const lvl=!Number.isFinite(dbm)?0:dbm>=-55?4:dbm>=-67?3:dbm>=-75?2:1;const label=['Desconhecido','Fraco','Razoável','Bom','Excelente'][lvl];return <span className={'signal s'+lvl} title={`${dbm} dBm`}><span className="bars">{[1,2,3,4].map(b=><i key={b} className={b<=lvl?'on':''}/>)}</span>{Number.isFinite(dbm)?`${dbm} dBm`:'—'} <small>{label}</small></span>}
export function StationsView({data,leases}:{data:Data,leases:Data[]}){
 if(!data?.clients_available)return <Panel title="Clientes conectados" icon={Users}><Empty>{data?.reason||'Consulta indisponível: o helper ainda não está ativo.'}</Empty></Panel>;
 const list=parseStations(String(data.clients||'')),ap=parseKV(String(data.hostapd||'')),lease=(mac:string)=>(leases||[]).find(l=>String(l.mac).toLowerCase()===mac);
 return <>
  {data.hostapd&&<div className="stat-grid four"><Stat icon={Wifi} label="ESTADO DO AP" value={ap.state==='ENABLED'?'Transmitindo':ap.state||'—'} tone={ap.state==='ENABLED'?'good':'warn'} detail={ap['ssid[0]']?`SSID ${ap['ssid[0]']}`:undefined}/><Stat icon={Radio} label="CANAL" value={ap.channel||'—'} detail={ap.freq?`${ap.freq} MHz`:undefined}/><Stat icon={Users} label="CLIENTES" value={list.length} detail="Estações associadas agora"/><Stat icon={Network} label="BSSID" value={<code>{ap['bssid[0]']||'—'}</code>} detail="Endereço do ponto de acesso"/></div>}
  <Panel title="Clientes conectados" icon={Users} actions={<span className="subtle">{list.length} {list.length===1?'estação':'estações'}</span>}>
   {list.length?<div className="table-scroll"><table><thead><tr><th>Dispositivo</th><th>Endereço IP</th><th>Sinal</th><th>Velocidade (TX / RX)</th><th>Tráfego</th><th>Conectado há</th><th>Inatividade</th></tr></thead><tbody>{list.map(s=>{const l=lease(s.mac);return <tr key={s.mac}>
    <td><strong className="dev-name">{l?.hostname&&l.hostname!=='*'?l.hostname:'Dispositivo sem nome'}</strong><small className="mono muted">{s.mac}</small></td>
    <td>{l?.ip?<code>{l.ip}</code>:<span className="muted">Sem concessão DHCP</span>}</td>
    <td><Signal dbm={parseInt(s.f['signal'])}/></td>
    <td>{(s.f['tx bitrate']||'—').split(' ').slice(0,2).join(' ')} / {(s.f['rx bitrate']||'—').split(' ').slice(0,2).join(' ')}</td>
    <td><span className="traffic"><ArrowDownLeft size={12}/>{fmtBytes(+s.f['tx bytes']||0)} <ArrowUpRight size={12}/>{fmtBytes(+s.f['rx bytes']||0)}</span></td>
    <td>{fmtDuration(parseInt(s.f['connected time']))}</td>
    <td>{s.f['inactive time']?fmtDuration(parseInt(s.f['inactive time'])/1000):'—'}</td></tr>})}</tbody></table></div>:<Empty>Nenhum dispositivo conectado ao ponto de acesso.</Empty>}
   <Raw value={data.clients} label="Ver saída do iw station dump"/>
  </Panel></>;
}

/* ---------- hosts reported to the ACS (TR-098 Hosts / AssociatedDevice) ---------- */
export function AcsHosts({request,notify}:{request:(p:string,m?:string,d?:unknown)=>Promise<any>,notify:(m:string)=>void}){
 const [data,setData]=useState<Data|null>(null),[count,setCount]=useState(0),[saving,setSaving]=useState(false);
 const load=()=>request('lab/hosts').then(d=>{setData(d);setCount(d.simulated_count)}).catch(e=>notify(e.message));
 React.useEffect(()=>{load()},[]);
 async function save(){setSaving(true);try{await request('lab/simulated-hosts','PUT',{count});notify(count?`${count} hosts simulados ativos. O ACS será avisado no próximo Inform.`:'Hosts simulados removidos.');await load()}catch(e){notify((e as Error).message)}finally{setSaving(false)}}
 const hosts:Data[]=data?.hosts||[];
 return <Panel title="Hosts reportados ao ACS" icon={Server} actions={<span className="subtle">LANDevice.1.Hosts.Host · AssociatedDevice</span>} className="spaced">
  <div className="sim-control"><div><strong>Hosts simulados</strong><small>Clientes fictícios adicionados só ao relatório TR-069 (marcados com X_LAB_Simulated=true). Não usam o rádio, o DHCP nem o firewall.</small></div><div className="sim-input"><input type="number" disabled={!data} min={0} max={data?.max_simulated??16} value={count} onChange={e=>setCount(Math.max(0,Math.min(Number(e.target.value)||0,data?.max_simulated??16)))}/><button className="primary" disabled={saving||!data||count===data.simulated_count} onClick={save}>{saving?<Clock size={15} className="spin"/>:<Check size={15}/>}Aplicar</button></div></div>
  {data?<div className="table-scroll"><table><thead><tr><th>#</th><th>Dispositivo</th><th>Endereço IP</th><th>Conexão</th><th>Sinal</th><th>Origem</th></tr></thead><tbody>{hosts.map((h,i)=><tr key={h.mac}>
   <td>{i+1}</td>
   <td><strong className="dev-name">{h.hostname||'Sem nome'}</strong><small className="mono muted">{h.mac}</small></td>
   <td>{h.ip?<code>{h.ip}</code>:<span className="muted">—</span>}</td>
   <td>{h.active?<Pill state={true} label={h.wireless?'Wi-Fi associado':'Ativo'}/>:<Pill state={false} label="Inativo (só lease)"/>}</td>
   <td>{h.wireless&&h.active?<Signal dbm={h.signal}/>:'—'}</td>
   <td>{h.simulated?<Tag cls="warn">Simulado</Tag>:<Tag cls="good">Real</Tag>}</td></tr>)}</tbody></table>{!hosts.length&&<Empty>Nenhum host para reportar.</Empty>}</div>:<Empty>Carregando…</Empty>}
 </Panel>;
}

/* ---------- PPPoE dialer + simulated BRAS ---------- */
const pppState:Record<string,[string,'good'|'warn'|'bad'|undefined]>={Connected:['Conectado','good'],Connecting:['Conectando…','warn'],Disconnected:['Desconectado','bad'],Unconfigured:['Desativado',undefined]};
const pppError:Record<string,string>={ERROR_NONE:'Nenhum erro',ERROR_AUTHENTICATION_FAILURE:'Falha de autenticação (usuário/senha recusados pelo BRAS)',ERROR_NO_ANSWER:'BRAS não respondeu à descoberta PPPoE (PADO/PADS)',ERROR_ISP_TIME_OUT:'BRAS parou de responder (LCP echo)',ERROR_ISP_DISCONNECT:'Sessão encerrada pelo BRAS',ERROR_IP_CONFIGURATION:'Falha na negociação de IP (IPCP)',ERROR_UNKNOWN:'Erro desconhecido'};
export function PPPoEPage({s,request,notify}:{s:Data|null,request:(p:string,m?:string,d?:unknown)=>Promise<any>,notify:(m:string)=>void}){
 const [cfg,setCfg]=useState<Data|null>(null),[bras,setBras]=useState<Data|null>(null),[busy,setBusy]=useState('');
 React.useEffect(()=>{request('pppoe').then(setCfg).catch(e=>notify(e.message));request('bras').then(setBras).catch(e=>notify(e.message))},[]);
 const st:Data=s?.network?.pppoe||{},[label,tone]=pppState[st.state]||[st.state||'—',undefined];
 async function save(path:string,body:Data,msg:string){setBusy(path);try{await request(path,'PUT',body);notify(msg);if(path==='pppoe')setCfg(await request('pppoe'))}catch(e){notify((e as Error).message)}finally{setBusy('')}}
 const setC=(k:string,v:any)=>setCfg(c=>({...c,[k]:v}));
 return <>
  <div className="notice"><Network size={18}/><div><strong>Discador PPPoE ligado a um BRAS simulado</strong><span>O concentrador roda isolado no namespace <code>lab-bras</code>, ligado à CPE por um cabo virtual (veth). A sessão PPPoE é real (descoberta, CHAP, IPCP), mas não substitui a rota padrão nem o DNS do notebook: a WAN Wi-Fi continua sendo a saída para a internet.</span></div></div>
  <div className="stat-grid four">
   <Stat icon={Activity} label="ESTADO DA SESSÃO" value={label} tone={tone} detail={st.state==='Connected'?`há ${fmtDuration(st.uptime_seconds)}`:pppError[st.last_error]||st.last_error}/>
   <Stat icon={Globe} label="IP RECEBIDO" value={st.local_ip||'—'} detail={st.remote_ip?`gateway ${st.remote_ip}`:'Atribuído pelo BRAS via IPCP'}/>
   <Stat icon={Server} label="CONCENTRADOR" value={st.ac_name||'—'} detail={cfg?.service_name?`Service-Name ${cfg.service_name}`:'Service-Name qualquer'}/>
   <Stat icon={ArrowDownLeft} label="TRÁFEGO DA SESSÃO" value={`${fmtBytes(st.rx_bytes||0)} / ${fmtBytes(st.tx_bytes||0)}`} detail={`recebido / enviado · MRU ${st.mru||'—'}`}/>
  </div>
  {st.last_error&&st.last_error!=='ERROR_NONE'&&<div className="diag-banner bad"><X size={18}/><div><strong>{pppError[st.last_error]||st.last_error}</strong><span>{st.detail||'Veja o motivo no log do pppd.'} · LastConnectionError = {st.last_error}</span></div></div>}
  <section className="panel form-panel spaced"><div className="panel-heading"><div><Lock size={18}/><h2>Discador PPPoE da CPE</h2></div><span className="subtle">WANPPPConnection.1</span></div>
   {cfg?<div className="form-body">
    <label className="toggle-row"><div><strong>Habilitar PPPoE</strong><small>Liga o BRAS simulado e disca automaticamente (reconecta sozinho se cair).</small></div><input type="checkbox" checked={!!cfg.enabled} onChange={e=>setC('enabled',e.target.checked)}/></label>
    <div className="form-grid">
     <label className="field">Usuário PPPoE<input value={cfg.username||''} onChange={e=>setC('username',e.target.value)} placeholder="cliente@provedor" autoComplete="off"/></label>
     <label className="field">Senha PPPoE<input type="password" value={cfg.password||''} onChange={e=>setC('password',e.target.value)} placeholder="Em branco mantém a senha atual" autoComplete="new-password"/><small>Sem espaços, aspas, # ou barra invertida.</small></label>
     <label className="field">Service-Name<input value={cfg.service_name||''} onChange={e=>setC('service_name',e.target.value)} placeholder="vazio = qualquer"/></label>
     <label className="field">MRU / MTU<input type="number" value={cfg.mru||''} onChange={e=>setC('mru',Number(e.target.value)||0)} placeholder="1492"/></label>
    </div>
    <p className="muted">O ACS também pode provisionar estes campos via TR-069 (<code>WANPPPConnection.1.Username</code>, <code>Password</code>, <code>Enable</code>, <code>PPPoEServiceName</code>, <code>MaxMRUSize</code>).</p>
   </div>:<Empty>Carregando…</Empty>}
   <div className="form-footer"><span><Lock size={14}/> Alterações auditadas</span><button className="primary" disabled={!cfg||busy!==''} onClick={()=>save('pppoe',cfg!,'Configuração PPPoE aplicada. O discador está reconectando.')}>{busy==='pppoe'?<Clock size={16} className="spin"/>:<Check size={16}/>}Salvar e discar</button></div></section>
  <section className="panel form-panel spaced"><div className="panel-heading"><div><Server size={18}/><h2>BRAS simulado · contas aceitas</h2></div><span className="subtle">namespace lab-bras · 100.127.250.1</span></div>
   {bras?<div className="form-body">
    <label className="field">Service-Name anunciado<input value={bras.service_name||''} onChange={e=>setBras({...bras,service_name:e.target.value})} placeholder="vazio = qualquer"/><small>Se o discador pedir outro Service-Name, a descoberta falha (útil para testar erro).</small></label>
    <div className="field">Contas PPPoE<small>O BRAS autentica por CHAP. Use uma senha diferente da configurada na CPE para simular falha de autenticação.</small></div>
    <ListEditor rows={bras.accounts} onChange={v=>setBras({...bras,accounts:v})} blank={{username:'',password:''}} addLabel="Adicionar conta" empty="Nenhuma conta: toda autenticação será recusada." columns={[{key:'username',label:'Usuário',placeholder:'cliente@provedor'},{key:'password',label:'Senha',placeholder:'senha'}]}/>
   </div>:<Empty>Carregando…</Empty>}
   <div className="form-footer"><span><Lock size={14}/> Contas valem na próxima autenticação</span><button className="primary" disabled={!bras||busy!==''} onClick={()=>save('bras',bras!,'Contas do BRAS atualizadas.')}>{busy==='bras'?<Clock size={16} className="spin"/>:<Check size={16}/>}Salvar contas</button></div></section>
 </>;
}

/* ---------- firewall (nft list) ---------- */
type Rule={cond:string,action:string,packets?:number,bytes?:number,comment?:string,raw:string};
type Chain={name:string,type?:string,hook?:string,priority?:string,policy?:string,rules:Rule[]};
type NftTable={family:string,name:string,chains:Chain[]};
const VERDICTS=['accept','drop','reject','masquerade','return','snat','dnat','redirect','jump','goto','log'];
export function parseNft(text:string){const tables:NftTable[]=[];let table:NftTable|null=null,chain:Chain|null=null;
 for(const raw of (text||'').split('\n')){const l=raw.trim();if(!l)continue;let m;
  if((m=/^table (\S+) (\S+) \{/.exec(l))){table={family:m[1],name:m[2],chains:[]};tables.push(table);chain=null;continue}
  if((m=/^chain (\S+) \{/.exec(l))){chain={name:m[1],rules:[]};table?.chains.push(chain);continue}
  if(l==='}'){if(chain)chain=null;else table=null;continue}
  if(!chain)continue;
  if((m=/^type (\S+) hook (\S+) priority ([^;]+);(?:\s*policy (\w+);)?/.exec(l))){Object.assign(chain,{type:m[1],hook:m[2],priority:m[3].trim(),policy:m[4]});continue}
  const c=/counter packets (\d+) bytes (\d+)/.exec(l),cm=/comment "([^"]*)"/.exec(l);
  const body=l.replace(/counter packets \d+ bytes \d+/,'').replace(/\bcounter\b/,'').replace(/comment "[^"]*"/,'').replace(/# handle \d+/,'').trim();
  const tokens=body.split(/\s+/),idx=tokens.findIndex(t=>VERDICTS.includes(t));
  chain.rules.push({cond:idx<0?body:tokens.slice(0,idx).join(' '),action:idx<0?'':tokens.slice(idx).join(' '),packets:c?+c[1]:undefined,bytes:c?+c[2]:undefined,comment:cm?.[1],raw:l});
 }
 return tables;
}
const ctState=(v:string)=>v.split(',').map(s=>({established:'estabelecida',related:'relacionada',new:'nova',invalid:'inválida',untracked:'não rastreada'} as Data)[s]||s).join(' / ');
function setValue(v:string){if(!v.startsWith('{'))return {text:v.replace(/"/g,'')};const items=v.slice(1,-1).split(',').map(s=>s.trim()).filter(Boolean);return items.length>3?{text:`${items.length} redes`,title:items.join('\n')}:{text:items.join(', ')}}
function Conditions({cond}:{cond:string}){
 if(!cond)return <span className="muted">Todo o tráfego</span>;
 const re=/(iifname|oifname)\s+(!=\s+)?("[^"]+"|\{[^}]*\}|\S+)|(?:ip6?|meta)\s+(saddr|daddr|nfproto|l4proto|protocol)\s+(!=\s+)?(\{[^}]*\}|\S+)|(tcp|udp)\s+(sport|dport)\s+(!=\s+)?(\{[^}]*\}|\S+)|ct state\s+(!=\s+)?(\S+)/g;
 const chips:{k:string,v:string,neg:boolean,title?:string}[]=[];let m,rest=cond;
 while((m=re.exec(cond))){rest=rest.replace(m[0],'');
  if(m[1])chips.push({k:m[1]==='iifname'?'Entrada':'Saída',neg:!!m[2],...{v:setValue(m[3]).text,title:setValue(m[3]).title}});
  else if(m[4]){const sv=setValue(m[6]);chips.push({k:m[4]==='saddr'?'Origem':m[4]==='daddr'?'Destino':'Protocolo',neg:!!m[5],v:sv.text,title:sv.title})}
  else if(m[7]){const sv=setValue(m[10]);chips.push({k:`${m[7].toUpperCase()} porta ${m[8]==='dport'?'destino':'origem'}`,neg:!!m[9],v:sv.text,title:sv.title})}
  else if(m[12])chips.push({k:'Conexão',neg:!!m[11],v:ctState(m[12])});
 }
 rest=rest.trim();
 return <span className="conds">{chips.map((c,i)=><span key={i} className="cond" title={c.title}><b>{c.k}</b>{c.neg?'≠ ':''}{c.v}</span>)}{rest&&<code className="cond raw-cond">{rest}</code>}</span>;
}
function Action({a}:{a:string}){const v=a.split(' ')[0];const map:Data={accept:['Permitir','good'],drop:['Bloquear','bad'],reject:['Rejeitar','bad'],masquerade:['NAT (masquerade)','info'],dnat:['Redirecionar','info'],snat:['NAT de origem','info'],redirect:['Redirecionar','info'],return:['Retornar',''],jump:['Ir para cadeia',''],goto:['Ir para cadeia',''],log:['Registrar','']};const [label,cls]=map[v]||[a||'Apenas contar',''];return <Tag cls={cls} title={a}>{label}{/^(dnat|snat|jump|goto|redirect)/.test(v)&&a.split(' ').length>1?` → ${a.split(' ').slice(-1)[0]}`:''}</Tag>}
const hookName=(h?:string)=>({input:'Tráfego destinado ao host',forward:'Tráfego encaminhado (clientes ↔ internet)',output:'Tráfego gerado pelo host',prerouting:'Antes do roteamento',postrouting:'Após o roteamento (NAT de saída)'} as Data)[h||'']||h||'Cadeia auxiliar';
export function FirewallRules({text}:{text:any}){
 if(!text||typeof text!=='string')return <Empty>Tabelas do laboratório ainda não instaladas.</Empty>;
 const tables=parseNft(text);if(!tables.length)return <pre className="code">{text}</pre>;
 return <div className="nft">{tables.map(t=><div key={t.family+t.name} className="nft-table"><div className="nft-title"><Shield size={15}/><strong>{t.name}</strong><Tag>{t.family==='inet'?'IPv4 + IPv6':t.family==='ip'?'IPv4':t.family}</Tag></div>
  {t.chains.map(c=><div key={c.name} className="nft-chain"><div className="nft-chain-head"><div><strong>{c.name}</strong><small>{hookName(c.hook)}{c.type?` · ${c.type==='nat'?'NAT':'filtro'}`:''}</small></div>{c.policy&&<span className="policy">Política padrão: <Tag cls={c.policy==='accept'?'good':'bad'}>{c.policy==='accept'?'Permitir':'Bloquear'}</Tag></span>}</div>
   {c.rules.length?<div className="table-scroll"><table><thead><tr><th>#</th><th>Condição</th><th>Ação</th><th>Pacotes</th><th>Volume</th></tr></thead><tbody>{c.rules.map((r,i)=><tr key={i} title={r.raw}><td>{i+1}</td><td><Conditions cond={r.cond}/>{r.comment&&<small className="muted block">{r.comment}</small>}</td><td><Action a={r.action}/></td><td className={r.packets?'hit':'muted'}>{r.packets===undefined?'—':fmtNum(r.packets)}</td><td className="muted">{r.bytes===undefined?'—':fmtBytes(r.bytes)}</td></tr>)}</tbody></table></div>:<p className="muted nft-empty">Sem regras nesta cadeia.</p>}
  </div>)}</div>)}<Raw value={text} label="Ver regras nft originais"/></div>;
}

/* ---------- services ---------- */
const serviceInfo:Record<string,[string,string]>={api:['API e painel web','Servidor HTTP deste painel'],helper:['Helper de rede','Aplica AP, DHCP e firewall com privilégios'],ap:['Ponto de acesso (hostapd)','Transmite a rede Wi-Fi do laboratório'],dhcp:['Servidor DHCP (dnsmasq)','Entrega endereços IP aos clientes'],cwmp:['Cliente TR-069 / CWMP','Sessões SOAP com o ACS']};
const serviceState=(v:string):[string,boolean|undefined]=>v==='active'?['Em execução',true]:v==='inactive'?['Parado',false]:v==='failed'?['Falhou',false]:v==='activating'?['Iniciando',undefined]:v==='deactivating'?['Parando',undefined]:[v||'Indisponível',undefined];
export function ServicesView({services}:{services:Data|undefined}){
 const entries=Object.entries(services||{});
 if(!entries.length)return <Empty>Estado dos serviços indisponível.</Empty>;
 const active=entries.filter(([,v])=>v==='active').length;
 return <><div className="services-summary"><strong>{active}</strong> de {entries.length} serviços em execução</div><div className="service-list flush">{entries.map(([k,v])=>{const [label,ok]=serviceState(String(v));const [name,desc]=serviceInfo[k]||[k,''];return <div className="service-row" key={k}><div className={'service-symbol '+(ok?'on':'')}><Server size={16}/></div><div><strong>{name}</strong><span>{desc} · <code>lab-router-{k}.service</code></span></div><Pill state={ok} label={label}/></div>})}</div></>;
}

/* ---------- editable list (replaces raw JSON editing) ---------- */
export type EditColumn={key:string,label:string,type?:'text'|'number'|'select',options?:{value:string,label:string}[],placeholder?:string,pattern?:string,hint?:string};
export function ListEditor({rows,columns,onChange,blank,addLabel,empty}:{rows:Data[]|null|undefined,columns:EditColumn[],onChange:(v:Data[])=>void,blank:Data,addLabel:string,empty:string}){
 const list=Array.isArray(rows)?rows:[];const set=(i:number,k:string,v:any)=>onChange(list.map((r,j)=>j===i?{...r,[k]:v}:r));
 return <div className="list-editor">{list.length>0&&<div className="table-scroll"><table><thead><tr>{columns.map(c=><th key={c.key}>{c.label}</th>)}<th/></tr></thead><tbody>{list.map((r,i)=><tr key={i}>{columns.map(c=><td key={c.key}>{c.type==='select'?<select value={r[c.key]??''} onChange={e=>set(i,c.key,e.target.value)}>{c.options!.map(o=><option key={o.value} value={o.value}>{o.label}</option>)}</select>:<input type={c.type==='number'?'number':'text'} value={c.type==='number'&&!r[c.key]?'':r[c.key]??''} placeholder={c.placeholder} pattern={c.pattern} title={c.hint} spellCheck={false} onChange={e=>set(i,c.key,c.type==='number'?(e.target.value===''?0:Number(e.target.value)):e.target.value)}/>}</td>)}<td className="row-actions"><button type="button" className="icon-button danger" title="Remover" onClick={()=>onChange(list.filter((_,j)=>j!==i))}><Trash2 size={15}/></button></td></tr>)}</tbody></table></div>}
  {!list.length&&<div className="list-empty">{empty}</div>}
  <button type="button" className="button" onClick={()=>onChange([...list,{...blank}])}><Plus size={15}/>{addLabel}</button></div>;
}
