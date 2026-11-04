import { useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Alert, Button, Card, ConfigProvider, Input, InputNumber, Modal, Progress, Select, Space, Spin, Table, Tag, Typography, message } from 'antd';
import 'antd/dist/reset.css';
import type { Detail, ScheduleSpec, ScheduleCreated, RevokeResult, Session, Device, Period } from '@/api/elx-management';

const { Title, Text } = Typography;
type Account = { id: string; email: string; csrf: string };
type Entitlements = {items: string[]};
type LKError = {error?:string};
const rawBase = window.X_UI_BASE_PATH || '/';
const base = rawBase.replace(/\/$/, '');
const endpoint = (path:string)=>base+'/lk/api'+path;
const formatMbps = (value:number)=>(value*8/1_000_000).toLocaleString('ru-RU',{maximumFractionDigits:2})+' Мбит/с';
const toBytes = (value:number)=>Math.floor(value*125_000);
const percent = (p:Period)=>{
 const used=p.direction==='up'?p.upBytes:p.direction==='down'?p.downBytes:p.upBytes+p.downBytes;
 return Math.min(100,Math.round(1000*used/p.thresholdBytes)/10);
};
async function request<T>(path:string,method:'GET'|'POST'='GET',payload?:unknown,csrf?:string):Promise<T>{
 const response=await fetch(endpoint(path),{
  method,credentials:'same-origin',
  headers:{
   ...(payload!==undefined?{'Content-Type':'application/json'}:{}),
   ...(csrf?{'X-ElX-CSRF':csrf}:{}),
  },
  body:payload===undefined?undefined:JSON.stringify(payload),
 });
 let result:unknown={};
 try{result=await response.json();}catch{}
 if(!response.ok){
  const error=(result as LKError)?.error;
  throw new Error(error||'Запрос не выполнен ('+response.status+')');
 }
 return result as T;
}
function LKApp(){
 const [apiMessage,holder]=message.useMessage();
 const [account,setAccount]=useState<Account|null>(null);
 const [mode,setMode]=useState<'login'|'register'>('login');
 const [email,setEmail]=useState('');
 const [password,setPassword]=useState('');
 const [invite,setInvite]=useState('');
 const [claimCode,setClaimCode]=useState('');
 const [items,setItems]=useState<string[]>([]);
 const [selected,setSelected]=useState('');
 const [detail,setDetail]=useState<Detail|null>(null);
 const [busy,setBusy]=useState(false);
 const [checking,setChecking]=useState(true);
 const [scheduleOpen,setScheduleOpen]=useState(false);
 const [cadence,setCadence]=useState<'day'|'week'|'month'>('month');
 const [direction,setDirection]=useState<'up'|'down'|'total'>('total');
 const [timezone,setTimezone]=useState('UTC');
 const [thresholdGB,setThresholdGB]=useState(10);
 const [reducedUp,setReducedUp]=useState(5);
 const [reducedDown,setReducedDown]=useState(5);
 const attempt=useRef<{fingerprint:string;id:string}|null>(null);

 async function loadItems(){
  const res=await request<Entitlements>('/entitlements');
  setItems(res.items);
  setSelected((old)=>res.items.includes(old)?old:(res.items[0]||''));
  if(!res.items.length){setDetail(null);}
 }
 async function refreshDetail(id:string){
  const data=await request<Detail>('/entitlements/'+encodeURIComponent(id));
  setDetail(data);
 }
 useEffect(()=>{
  let valid=true;
  request<Account>('/me').then(async a=>{
   if(!valid)return;
   setAccount(a);
   try{await loadItems();}catch{if(valid)apiMessage.error('Не удалось загрузить подписки');}
  }).catch(()=>{if(valid)setAccount(null);})
    .finally(()=>{if(valid)setChecking(false);});
  return ()=>{valid=false;};
 },[]);
 useEffect(()=>{
  if(!account||!selected){setDetail(null);return;}
  let live=true;
  request<Detail>('/entitlements/'+encodeURIComponent(selected)).then(data=>{
   if(live)setDetail(data);
  }).catch(err=>{if(live){setDetail(null);apiMessage.error(String(err));}});
  return ()=>{live=false;};
 },[account?.id,selected]);

 async function submitAuth(){
  if(busy)return;
  setBusy(true);
  try{
   const data=mode==='login'?{email,password}:{email,password,invite};
   const a=await request<Account>(mode==='login'?'/login':'/register','POST',data);
   setPassword('');setInvite('');
   setAccount(a);
   await loadItems();
   apiMessage.success('Вход выполнен');
  }catch(e){apiMessage.error(e instanceof Error?e.message:'Ошибка входа');}
  finally{setBusy(false);}
 }
 async function logout(){
  if(!account)return;
  setBusy(true);
  try{await request('/logout','POST',undefined,account.csrf);}
  catch(e){apiMessage.error(e instanceof Error?e.message:'Ошибка выхода');setBusy(false);return;}
  setAccount(null);setSelected('');setItems([]);setDetail(null);setBusy(false);
 }
 async function claim(){
  if(!account||!claimCode||busy)return;
  setBusy(true);
  try{
   await request('/claim','POST',{invite:claimCode},account.csrf);
   setClaimCode('');await loadItems();
   apiMessage.success('Подписка добавлена');
  }catch(e){apiMessage.error(e instanceof Error?e.message:'Не удалось добавить подписку');}
  finally{setBusy(false);}
 }
 async function mutate(path:string){
  if(!account||!selected||busy)return;
  setBusy(true);
  try{
   await request(path,'POST',undefined,account.csrf);
   await refreshDetail(selected);
   await loadItems();
   apiMessage.info('Запрос принят. Освобождение слота требует подписанного подтверждения ElXray.');
  }catch(e){apiMessage.error(e instanceof Error?e.message:'Ошибка управления');}
  finally{setBusy(false);}
 }
 const drain=(s:Session)=>{
  if(s.state==='released'||s.state==='draining')return;
  Modal.confirm({
   title:'Отключить соединение?',
   content:'Слот будет занят до подписанного подтверждения ElXray, даже если соединение сейчас не передаёт данные.',
   okText:'Отключить',okButtonProps:{danger:true},cancelText:'Отмена',
   onOk:()=>mutate('/entitlements/'+selected+'/sessions/'+s.id+'/drain'),
  });
 };
 const revoke=(d:Device)=>{
  if(d.revoked)return;
  Modal.confirm({
   title:'Отозвать устройство?',
   content:'Новые подключения будут запрещены, а существующие переданы агенту ElXray на завершение. Неизвестные сессии останутся занятыми.',
   okText:'Отозвать',okButtonProps:{danger:true},cancelText:'Отмена',
   onOk:async()=>{
    if(!account||!selected)return;
    setBusy(true);
    try{
     const result=await request<RevokeResult>('/entitlements/'+selected+'/devices/'+d.id+'/revoke','POST',undefined,account.csrf);
     if(result.undrainable>0)apiMessage.warning('Устройство отозвано; '+result.undrainable+' сессий требуют ручного восстановления.');
     else apiMessage.info('Устройство отозвано; '+result.drainQueued+' сессий ожидают завершения.');
     await refreshDetail(selected);
     await loadItems();
    }catch(e){apiMessage.error(e instanceof Error?e.message:'Не удалось отозвать устройство');}
    finally{setBusy(false);}
   },
  });
 };
 async function createSchedule(){
  if(!account||!detail||!detail.rateBudget||busy)return;
  const thresholdBytes=Math.round(thresholdGB*1_000_000_000);
  const spec={cadence,direction,timezone,thresholdBytes,
   reducedUp:toBytes(reducedUp),reducedDown:toBytes(reducedDown)};
  if(!Number.isSafeInteger(thresholdBytes)||thresholdBytes<=0||
   spec.reducedUp<1||spec.reducedDown<1||
   spec.reducedUp>detail.rateBudget.up||spec.reducedDown>detail.rateBudget.down){
   apiMessage.error('Значения должны быть положительными и не превышать права подписки.');
   return;
  }
  const fingerprint=JSON.stringify({selected,...spec});
  if(!attempt.current||attempt.current.fingerprint!==fingerprint){
   attempt.current={fingerprint,id:crypto.randomUUID()};
  }
  const requestSpec:ScheduleSpec={scheduleId:attempt.current.id,...spec};
  setBusy(true);
  try{
   const result=await request<ScheduleCreated>('/entitlements/'+selected+'/fup-schedules','POST',requestSpec,account.csrf);
   attempt.current=null;
   setScheduleOpen(false);
   await refreshDetail(selected);
   apiMessage.success(result.alreadyExisted?'Расписание уже существует':'Расписание FUP создано');
  }catch(e){apiMessage.error(e instanceof Error?e.message:'Ошибка создания FUP');}
  finally{setBusy(false);}
 }
 if(checking)return <div style={{padding:40,textAlign:'center'}}><Spin size="large"/></div>;
 return <ConfigProvider>
  {holder}
  <div style={{maxWidth:1050,margin:'0 auto',padding:'24px 16px'}}>
   <Space style={{width:'100%',justifyContent:'space-between',marginBottom:12}} wrap>
    <Title level={3} style={{margin:0}}>ElectriumX — личный кабинет</Title>
    {account?<Space><Text type="secondary">{account.email}</Text><Button loading={busy} onClick={logout}>Выйти</Button></Space>:null}
   </Space>
   {!account?<Card style={{maxWidth:450,margin:'40px auto'}} title={mode==='login'?'Вход':'Регистрация по приглашению'}>
    <Space direction="vertical" style={{width:'100%'}} size="middle">
     <Input type="email" placeholder="E-mail" value={email} onChange={e=>setEmail(e.target.value)}/>
     <Input.Password placeholder="Пароль" autoComplete={mode==='login'?'current-password':'new-password'} value={password} onChange={e=>setPassword(e.target.value)} onPressEnter={submitAuth}/>
     {mode==='register'&&<Input.Password placeholder="Код приглашения" value={invite} onChange={e=>setInvite(e.target.value)}/>}
     <Button block type="primary" loading={busy} onClick={submitAuth}>{mode==='login'?'Войти':'Зарегистрироваться'}</Button>
     <Button type="link" onClick={()=>setMode(mode==='login'?'register':'login')}>{mode==='login'?'У меня есть приглашение':'Уже есть аккаунт'}</Button>
    </Space>
   </Card>:<>
    <Alert type="info" showIcon title="Управление подписками"
     description="Ссылка на подписку предоставляет только подключение и диагностику. Отключение устройств и управление FUP доступны после входа в этот аккаунт."/>
    <Card size="small" title="Ваши подписки" style={{marginTop:16}}>
     <Space wrap>
      {items.map(id=><Button key={id} type={selected===id?'primary':'default'} onClick={()=>setSelected(id)}>{id.slice(0,8)}…</Button>)}
     </Space>
     <Space style={{display:'flex',marginTop:12}} wrap>
      <Input placeholder="Добавить подписку по приглашению" value={claimCode} onChange={e=>setClaimCode(e.target.value)} style={{width:320}}/>
      <Button loading={busy} onClick={claim} disabled={!claimCode}>Добавить</Button>
     </Space>
    </Card>
    {selected&&detail&&<Space direction="vertical" size="middle" style={{width:'100%',marginTop:16}}>
     <Card title="Права подписки" extra={<Button onClick={()=>refreshDetail(selected)}>Обновить</Button>} size="small">
      <Space direction="vertical">
       <Text copyable>Подписка: {selected}</Text>
       <Text>Состояние: <Tag color={detail.entitlement.enabled?'success':'error'}>{detail.entitlement.enabled?'Активна':'Отключена'}</Tag></Text>
       <Text>Действует до: {new Date(detail.entitlement.validUntil).toLocaleString('ru-RU')}</Text>
       <Text>Занято слотов: {detail.entitlement.occupied} из {detail.entitlement.activeSlots}</Text>
       <Text>Общая скорость: ↑ {detail.rateBudget?formatMbps(detail.rateBudget.up):'не задана'} / ↓ {detail.rateBudget?formatMbps(detail.rateBudget.down):'не задана'}</Text>
      </Space>
     </Card>
     <Card title="Устройства" size="small">
      <Table<Device> rowKey="id" dataSource={detail.devices} pagination={false} size="small" scroll={{x:600}}
       columns={[
        {title:'Устройство',dataIndex:'id',render:(id:string)=><Text copyable>{id}</Text>},
        {title:'Статус',dataIndex:'revoked',render:(v:boolean)=><Tag color={v?'error':'success'}>{v?'Отозвано':'Действует'}</Tag>},
        {title:'',render:(_,device)=><Button danger size="small" disabled={device.revoked||busy} onClick={()=>revoke(device)}>Отключить устройство</Button>},
       ]}/>
     </Card>
     <Card title="VPN-сессии" size="small">
      {detail.sessionsTruncated&&<Alert type="warning" title="Показаны последние 200 записей"/>}
      <Table<Session> rowKey="id" dataSource={detail.sessions} pagination={false} size="small" scroll={{x:700}}
       columns={[
        {title:'ID сессии',dataIndex:'id',render:(id:string)=><Text copyable>{id}</Text>},
        {title:'Нода',dataIndex:'nodeId'},
        {title:'Статус',dataIndex:'state',render:(v:string)=><Tag>{v}</Tag>},
        {title:'',render:(_,s)=><Button danger size="small" onClick={()=>drain(s)} disabled={busy||s.state==='draining'||s.state==='released'}>Отключить</Button>},
       ]}/>
     </Card>
     <Card size="small" title="FUP" extra={<Button disabled={busy||!detail.rateBudget} onClick={()=>setScheduleOpen(true)}>Создать правило</Button>}>
      <Alert type="warning" showIcon title="Для FUP требуется подтверждённый учёт трафика"
       description="Правило начнёт ограничивать трафик только при корректной привязке доверенных потоков учёта и работающем агенте Control."/>
      {detail.periods.map(p=><Card key={p.id} size="small" style={{marginTop:10}} title={(p.cadence||'Период')+' — '+p.direction}
       extra={p.fupActive?<Tag color="warning">Снижение активно</Tag>:null}>
       <Text type="secondary">{new Date(p.startsAt).toLocaleString('ru-RU')} — {new Date(p.endsAt).toLocaleString('ru-RU')}</Text>
       <Progress percent={percent(p)}/>
       <div>Порог: {(p.thresholdBytes/1_000_000_000).toLocaleString('ru-RU')} ГБ</div>
       <div>После порога: ↑ {formatMbps(p.reducedUp)} / ↓ {formatMbps(p.reducedDown)}</div>
       {p.scheduleId && (() => {
        const coverage = (detail.usageCoverage ?? []).find(x => x.scheduleId === p.scheduleId);
        return coverage?.ready
         ? <Alert type="success" showIcon title="Источники подписанного учёта подключены" description="Свежие подписанные показания получены. Отдельно проверьте обязательный lease и применение политики ElXray."/>
         : <Alert type="warning" showIcon title="Учёт трафика ещё не готов" description={coverage
          ? 'Привязано: '+coverage.assignedGrants+' из '+coverage.expectedGrants+', исходный замер: '+coverage.primedGrants+', свежие подписанные показания: '+coverage.freshGrants
          : 'Нет подтверждённых назначений потоков. Нельзя считать FUP действующим.'}/>;
       })()}
      </Card>)}
     </Card>
    </Space>}
   </>}
  </div>
  <Modal title="Новое правило FUP" open={scheduleOpen} okText="Создать" cancelText="Отмена" onOk={createSchedule}
   okButtonProps={{loading:busy}} onCancel={()=>setScheduleOpen(false)}>
   <Space direction="vertical" style={{width:'100%'}}>
    <Text>Период</Text>
    <Select value={cadence} onChange={v=>setCadence(v as 'day'|'week'|'month')}
     options={[{value:'day',label:'День'},{value:'week',label:'Неделя'},{value:'month',label:'Месяц'}]}/>
    <Text>Часовой пояс IANA</Text>
    <Input value={timezone} onChange={e=>setTimezone(e.target.value)} placeholder="Europe/Moscow"/>
    <Text>Считать трафик</Text>
    <Select value={direction} onChange={v=>setDirection(v as 'up'|'down'|'total')}
     options={[{value:'total',label:'В обоих направлениях'},{value:'up',label:'Upload'},{value:'down',label:'Download'}]}/>
    <Text>Порог (ГБ)</Text><InputNumber min={0.01} style={{width:'100%'}} value={thresholdGB} onChange={v=>setThresholdGB(Number(v??0))}/>
    <Text>Снижение upload (Мбит/с)</Text><InputNumber min={0.01} style={{width:'100%'}} value={reducedUp} onChange={v=>setReducedUp(Number(v??0))}/>
    <Text>Снижение download (Мбит/с)</Text><InputNumber min={0.01} style={{width:'100%'}} value={reducedDown} onChange={v=>setReducedDown(Number(v??0))}/>
   </Space>
  </Modal>
 </ConfigProvider>;
}
const root=document.getElementById('app');
if(root)createRoot(root).render(<LKApp/>);
