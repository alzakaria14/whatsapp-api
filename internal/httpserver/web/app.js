const state={numbers:[],view:'numbers',qrTimer:null};
const $=(selector,root=document)=>root.querySelector(selector);
const $$=(selector,root=document)=>[...root.querySelectorAll(selector)];

async function request(path,options={}){
  const response=await fetch(path,{...options,headers:{'Content-Type':'application/json',...(options.headers||{})}});
  if(response.status===204)return null;
  const data=await response.json().catch(()=>({error:'Respons server tidak valid'}));
  if(!response.ok)throw new Error(data.error||`HTTP ${response.status}`);
  return data;
}

function escapeHTML(value=''){const node=document.createElement('span');node.textContent=value;return node.innerHTML}
function formatDate(value){if(!value)return '—';return new Intl.DateTimeFormat('id-ID',{dateStyle:'medium',timeStyle:'short'}).format(new Date(value))}
function badge(value){return `<span class="badge ${escapeHTML(value)}">${escapeHTML(value.replace('_',' '))}</span>`}
function phone(value){return value?`+${value}`:'Belum terhubung'}
function icon(){return '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="7" y="2" width="10" height="20" rx="2"/><path d="M11 18h2"/></svg>'}
function renderSenders(){
  const select=$('#send-from');
  const connected=state.numbers.filter(number=>number.status==='connected'&&number.phone);
  select.innerHTML=connected.length?connected.map(number=>`<option value="${escapeHTML(number.phone)}">${escapeHTML(number.name)} (+${escapeHTML(number.phone)})</option>`).join(''):'<option value="">Tidak ada nomor yang terhubung</option>';
  select.disabled=!connected.length;
}

async function loadNumbers(){
  const grid=$('#numbers-grid');grid.innerHTML='<div class="empty"><p>Memuat nomor…</p></div>';
  try{
    const result=await request('/dashboard/api/numbers');state.numbers=result.data;renderSenders();
    if(!state.numbers.length){grid.innerHTML=`<div class="empty"><div class="empty-icon">${icon()}</div><h3>Belum ada nomor</h3><p>Tambahkan nomor pertama dan scan QR dari aplikasi WhatsApp.</p></div>`;return}
    grid.innerHTML=state.numbers.map(n=>`<article class="number-card" data-id="${n.id}">
      <div class="card-top"><div class="device-title"><div class="device-icon">${icon()}</div><div><h3>${escapeHTML(n.name)}</h3><p>${escapeHTML(phone(n.phone))}</p></div></div>${badge(n.status)}</div>
      <div class="meta"><div class="meta-row"><span>Webhook</span><code title="${escapeHTML(n.webhook_url||'')}">${escapeHTML(n.webhook_url||'Belum diatur')}</code></div><div class="meta-row"><span>Terhubung</span><span>${formatDate(n.connected_at)}</span></div></div>
      ${n.last_error?`<p class="card-error">${escapeHTML(n.last_error)}</p>`:''}
      <div class="card-actions">${n.status!=='connected'?'<button class="button primary pair" type="button">Tampilkan QR</button>':''}<button class="button ghost edit" type="button">Pengaturan</button><button class="button danger remove" type="button">Hapus</button></div>
    </article>`).join('');
  }catch(error){grid.innerHTML=`<div class="empty"><h3>Gagal memuat data</h3><p>${escapeHTML(error.message)}</p></div>`}
}

async function loadMessages(){
  const body=$('#messages-body');body.innerHTML='<tr><td colspan="6" class="table-empty">Memuat pesan…</td></tr>';
  try{const result=await request('/dashboard/api/messages?limit=100');const byId=Object.fromEntries(state.numbers.map(n=>[n.id,n]));
    body.innerHTML=result.data.length?result.data.map(m=>`<tr><td>${formatDate(m.created_at)}</td><td>${badge(m.direction==='inbound'?'received':'sent')}</td><td>${escapeHTML(byId[m.number_id]?.name||m.number_id)}</td><td><code>+${escapeHTML(m.remote_phone)}</code></td><td class="message-cell" title="${escapeHTML(m.text)}">${escapeHTML(m.text||`[${m.type}]`)}</td><td>${badge(m.status)}${m.error?`<small class="card-error">${escapeHTML(m.error)}</small>`:''}</td></tr>`).join(''):'<tr><td colspan="6" class="table-empty">Belum ada pesan.</td></tr>';
  }catch(error){body.innerHTML=`<tr><td colspan="6" class="table-empty">${escapeHTML(error.message)}</td></tr>`}
}

async function loadWebhooks(){
  const body=$('#webhooks-body');body.innerHTML='<tr><td colspan="6" class="table-empty">Memuat webhook…</td></tr>';
  try{const result=await request('/dashboard/api/webhooks?limit=100');body.innerHTML=result.data.length?result.data.map(d=>`<tr><td>${formatDate(d.created_at)}</td><td><code title="${escapeHTML(d.webhook_url)}">${escapeHTML(d.webhook_url)}</code>${d.last_error?`<small class="card-error">${escapeHTML(d.last_error)}</small>`:''}</td><td>${badge(d.status)}</td><td>${d.attempts}</td><td>${d.response_code||'—'}</td><td>${d.status==='failed'?`<button class="button ghost retry" data-id="${d.id}" type="button">Kirim ulang</button>`:'—'}</td></tr>`).join(''):'<tr><td colspan="6" class="table-empty">Belum ada pengiriman webhook.</td></tr>'}catch(error){body.innerHTML=`<tr><td colspan="6" class="table-empty">${escapeHTML(error.message)}</td></tr>`}
}

function switchView(view){state.view=view;$$('.tab').forEach(t=>t.classList.toggle('active',t.dataset.view===view));$$('.view').forEach(v=>v.classList.toggle('active',v.id===`view-${view}`));if(view==='messages')loadMessages();if(view==='webhooks')loadWebhooks()}
function toast(message,isError=false){const el=$('#toast');el.textContent=message;el.className=`toast show${isError?' error':''}`;clearTimeout(el.timer);el.timer=setTimeout(()=>el.className='toast',3000)}

function showQR(id,restart=false){
  const dialog=$('#qr-dialog'),image=$('#qr-image');dialog.showModal();image.removeAttribute('src');
  const poll=async()=>{try{if(restart){await request(`/dashboard/api/numbers/${id}/pair`,{method:'POST'});restart=false}const numbers=await request('/dashboard/api/numbers');const current=numbers.data.find(n=>n.id===id);if(current?.status==='connected'){closeQR();toast('Nomor berhasil terhubung');loadNumbers();return}image.src=`/dashboard/api/numbers/${id}/qr?t=${Date.now()}`}catch(error){if(!error.message.includes('QR belum'))toast(error.message,true)}};
  poll();state.qrTimer=setInterval(poll,2500);
}
function closeQR(){clearInterval(state.qrTimer);state.qrTimer=null;$('#qr-dialog').close()}

document.addEventListener('click',async event=>{
  const tab=event.target.closest('.tab');if(tab)switchView(tab.dataset.view);
  if(event.target.closest('#open-add'))$('#add-dialog').showModal();
  if(event.target.closest('.close-dialog'))$('#add-dialog').close();if(event.target.closest('.close-edit'))$('#edit-dialog').close();if(event.target.closest('.close-qr'))closeQR();
  if(event.target.closest('.refresh')){await loadNumbers();switchView(state.view)}
  const card=event.target.closest('.number-card');if(card){const item=state.numbers.find(n=>n.id===card.dataset.id);if(event.target.closest('.pair'))showQR(item.id,item.status!=='pairing');if(event.target.closest('.edit')){const form=$('#edit-form');form.elements.id.value=item.id;form.elements.name.value=item.name;form.elements.webhook_url.value=item.webhook_url||'';$('#edit-dialog').showModal()}if(event.target.closest('.remove')&&confirm(`Hapus ${item.name} dan seluruh riwayatnya?`)){try{await request(`/dashboard/api/numbers/${item.id}`,{method:'DELETE'});toast('Nomor dihapus');loadNumbers()}catch(error){toast(error.message,true)}}}
  const retry=event.target.closest('.retry');if(retry){try{await request(`/dashboard/api/webhooks/${retry.dataset.id}/retry`,{method:'POST'});toast('Webhook dijadwalkan ulang');loadWebhooks()}catch(error){toast(error.message,true)}}
});

$('#add-form').addEventListener('submit',async event=>{event.preventDefault();const form=event.currentTarget,button=$('button[type=submit]',form);button.disabled=true;$('#add-error').textContent='';try{const result=await request('/dashboard/api/numbers',{method:'POST',body:JSON.stringify({name:form.elements.name.value,webhook_url:form.elements.webhook_url.value})});form.reset();$('#add-dialog').close();await loadNumbers();showQR(result.data.id,false)}catch(error){$('#add-error').textContent=error.message}finally{button.disabled=false}});
$('#edit-form').addEventListener('submit',async event=>{event.preventDefault();const form=event.currentTarget;$('#edit-error').textContent='';try{await request(`/dashboard/api/numbers/${form.elements.id.value}`,{method:'PATCH',body:JSON.stringify({name:form.elements.name.value,webhook_url:form.elements.webhook_url.value})});$('#edit-dialog').close();toast('Pengaturan tersimpan');loadNumbers()}catch(error){$('#edit-error').textContent=error.message}});
$('#send-form').addEventListener('submit',async event=>{
  event.preventDefault();
  const form=event.currentTarget,button=$('button[type=submit]',form),result=$('#send-result');
  button.disabled=true;button.lastChild.textContent=' Mengirim…';$('#send-error').textContent='';result.className='send-result';result.textContent='';
  try{
    const response=await request('/dashboard/api/messages',{method:'POST',body:JSON.stringify({from:form.elements.from.value,to:form.elements.to.value,text:form.elements.text.value})});
    result.textContent=`Terkirim · ID ${response.data.wa_message_id}`;result.className='send-result show';form.elements.text.value='';toast('Pesan berhasil dikirim');await loadMessages();
  }catch(error){$('#send-error').textContent=error.message;result.textContent='Pengiriman gagal';result.className='send-result error'}
  finally{button.disabled=false;button.lastChild.textContent=' Kirim pesan'}
});
$('#qr-dialog').addEventListener('close',()=>{clearInterval(state.qrTimer);state.qrTimer=null});
loadNumbers();

