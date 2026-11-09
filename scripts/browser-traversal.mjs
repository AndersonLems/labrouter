import { chromium } from '../web/node_modules/playwright-core/index.mjs';
import {execFileSync} from 'node:child_process';
const browser=await chromium.launch({executablePath:'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
const page=await browser.newPage({viewport:{width:1512,height:1000}});const errors=[];page.on('pageerror',e=>errors.push(e.message));
await page.goto('http://127.0.0.1:8088');
await page.getByLabel('Senha administrativa',{exact:true}).fill(execFileSync('sudo',['-n','cat','/var/lib/lab-router/initial-admin-password'],{encoding:'utf8'}).trim());
await page.getByRole('button',{name:'Entrar no painel'}).click();await page.getByRole('heading',{name:'Dashboard',exact:true}).waitFor();
await page.getByTitle('TR-069 / CWMP').click();
for(const [tab,heading,file] of [['Diagnóstico TR-069','TR-069 / SOAP','basic'],['Diagnóstico Periodic Inform','Periodic Inform','periodic'],['Diagnóstico XMPP','XMPP Connection Request','xmpp'],['Diagnóstico STUN / UDP','STUN / UDP Connection Request','stun']]){
 await page.getByRole('button',{name:tab,exact:true}).click();await page.getByRole('heading',{name:heading,exact:true}).waitFor();await page.screenshot({path:`artifacts/diagnostic-${file}.png`,fullPage:true});
}
await page.getByRole('button',{name:'XMPP',exact:true}).click();await page.getByLabel('Conta XMPP (usuário ou JID)',{exact:true}).waitFor();
await page.getByRole('button',{name:'STUN / NAT Traversal',exact:true}).click();await page.getByLabel(/Versão STUN/).waitFor();
await page.getByRole('button',{name:'Diagnóstico STUN / UDP',exact:true}).click();await page.getByRole('heading',{name:'STUN / UDP Connection Request',exact:true}).waitFor();await page.setViewportSize({width:390,height:844});
await page.screenshot({path:'artifacts/diagnostic-mobile.png',fullPage:true});if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))errors.push('mobile overflow');
await browser.close();console.log(JSON.stringify({errors,pages:4,forms:2}));if(errors.length)process.exitCode=1;
