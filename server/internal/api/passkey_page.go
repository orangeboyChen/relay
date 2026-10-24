package api

import "net/http"

const passkeyPage = `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sim Deck Passkey</title><button id="go">Continue with passkey</button><p id="status"></p><script>
const status=document.getElementById('status'); const params=new URLSearchParams(location.search); const mode=params.get('mode')==='register'?'register':'login'; const operation=mode==='register'?'PasskeyRegistration':'PasskeyLogin';
function decode(value){value=value.replace(/-/g,'+').replace(/_/g,'/');return Uint8Array.from(atob(value+'='.repeat((4-value.length%4)%4)),c=>c.charCodeAt(0)).buffer}
function encode(value){return btoa(String.fromCharCode(...new Uint8Array(value))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'')}
function encodeBytes(value){return btoa(String.fromCharCode(...new Uint8Array(value)))}
function decodeOptions(value){return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(value),c=>c.charCodeAt(0))))}
function convert(value){if(Array.isArray(value))return value.map(convert);if(value&&typeof value==='object'){const result={};for(const [key,item] of Object.entries(value)){result[key]=['challenge','user','id'].includes(key)&&typeof item==='string'?decode(item):convert(item)}return result}return value}
async function rpc(method,body,headers){const response=await fetch('/relay.communication.v1.AuthenticationService/'+method,{method:'POST',headers,body:JSON.stringify(body)});const result=await response.json();if(!response.ok)throw new Error(result.message||'Passkey ceremony failed');return result}
document.getElementById('go').onclick=async()=>{try{const token=location.hash.startsWith('#token=')?decodeURIComponent(location.hash.slice(7)):'';const headers={'content-type':'application/json'};if(token)headers.authorization='Bearer '+token;const begin=await rpc('Begin'+operation,{},headers);const options=convert(decodeOptions(begin.optionsJson));const credential=mode==='register'?await navigator.credentials.create(options):await navigator.credentials.get(options);const body={id:credential.id,rawId:encode(credential.rawId),type:credential.type,response:{clientDataJSON:encode(credential.response.clientDataJSON)}};if(mode==='register')body.response.attestationObject=encode(credential.response.attestationObject);else{body.response.authenticatorData=encode(credential.response.authenticatorData);body.response.signature=encode(credential.response.signature);if(credential.response.userHandle)body.response.userHandle=encode(credential.response.userHandle)}const finish=await rpc('Finish'+operation,{ceremonyId:begin.ceremonyId,credentialJson:encodeBytes(new TextEncoder().encode(JSON.stringify(body)))},headers);const session=mode==='login'?finish:{};location.href='simdeck://passkey#'+encodeURIComponent(JSON.stringify(session));}catch(error){status.textContent=error.message||'Passkey ceremony failed'}};
</script>`

func (router *router) passkeyPage(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write([]byte(passkeyPage))
}
