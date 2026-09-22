function recoveryToken(locationValue, historyValue) {
  var fragment=String(locationValue.hash||"").replace(/^#/,""), match=/(?:^|&)token=([^&]*)/.exec(fragment), token="";
  try { token=match?decodeURIComponent(match[1]):""; } catch (_) {}
  historyValue.replaceState(null,"",String(locationValue.pathname||"/staff-mfa-recovery.html")+String(locationValue.search||"")); return token;
}
function recoveryRequest(path, body) { return {url:path,method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/json","Accept":"application/json"},body:JSON.stringify(body)}; }
if (typeof module!=="undefined"&&module.exports) module.exports={recoveryToken:recoveryToken,recoveryRequest:recoveryRequest};
if (typeof window!=="undefined") (function () { "use strict";
  var token=recoveryToken(window.location,window.history), form=document.querySelector("#staff-recovery-form"), message=document.querySelector("#staff-recovery-message"), error=document.querySelector("#staff-recovery-error"), setup=document.querySelector("#staff-recovery-setup"), key=document.querySelector("#staff-recovery-key"), uri=document.querySelector("#staff-recovery-uri"), qr=document.querySelector("#staff-recovery-qr"), generic="This recovery link is invalid or has expired.";
  function invalid(){message.textContent=generic;error.textContent=generic;form.hidden=true;setup.hidden=true;}
  if(!token){invalid();return;}
  var prepare=recoveryRequest("/api/v1/staff-mfa-recoveries/prepare",{token:token});
  fetch(prepare.url,prepare).then(function(r){if(!r.ok)throw new Error();return r.json();}).then(function(p){var m=p&&p.mfa_setup;if(!m||!m.manual_key||!m.uri||!m.qr_code)throw new Error();key.value=m.manual_key;uri.value=m.uri;qr.src=m.qr_code;qr.hidden=false;message.textContent="Scan the QR code, then enter its six-digit code.";setup.hidden=false;form.hidden=false;}).catch(invalid);
  form.addEventListener("submit",function(e){e.preventDefault();var b=form.querySelector("button[type=submit]");if(b.disabled)return;b.disabled=true;error.textContent="";var request=recoveryRequest("/api/v1/staff-mfa-recoveries/complete",{token:token,mfa_code:form.elements.mfa_code.value});fetch(request.url,request).then(function(r){if(!r.ok)throw new Error();form.reset();key.value="";uri.value="";window.location.replace("/");}).catch(function(){error.textContent="We could not finish setup. Enter a fresh six-digit authenticator code or ask an administrator for a new reset.";}).finally(function(){b.disabled=false;});});
}());
