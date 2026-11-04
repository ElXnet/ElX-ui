import { useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, ConfigProvider, Input, InputNumber, Layout, Modal, Progress, Select, Space, Spin, Table, Tag, Typography, message } from 'antd';
import AppSidebar from '@/layouts/AppSidebar';
import { useTheme } from '@/hooks/useTheme';
import { bitsToBytesPerSecond, elxManagement, mbps, occupancy, percentUsed } from '@/api/elx-management';
import type { Entitlement, Session, Device } from '@/api/elx-management';
const { Title, Text } = Typography;
export default function ElxManagementPage() {
 const {antdThemeConfig}=useTheme();
 const client=useQueryClient();
 const [selected,setSelected]=useState('');
 const [cursor,setCursor]=useState('');
 const [history,setHistory]=useState<string[]>([]);
 const [busy,setBusy]=useState<string|null>(null);
 const [fupOpen,setFupOpen]=useState(false);
 const [cadence,setCadence]=useState<'day'|'week'|'month'>('month');
 const [direction,setDirection]=useState<'up'|'down'|'total'>('total');
 const [timezone,setTimezone]=useState('UTC');
 const [thresholdGB,setThresholdGB]=useState(10);
 const [reducedUp,setReducedUp]=useState(5);
 const [reducedDown,setReducedDown]=useState(5);
 const scheduleAttempt=useRef<{fingerprint:string;id:string}|null>(null);

 const [messages,contextHolder]=message.useMessage();
 const [modal,modalHolder]=Modal.useModal();
 const listing=useQuery({queryKey:['elx','list',cursor],queryFn:()=>elxManagement.list(cursor),retry:false});
 const detail=useQuery({queryKey:['elx','detail',selected],queryFn:()=>elxManagement.get(selected),enabled:!!selected,retry:false});
 const refresh=async()=>{await client.invalidateQueries({queryKey:['elx']});};
 const drain=(session:Session)=>{
  if (!selected || busy || session.state==='released' || session.state==='draining') return;
  modal.confirm({
   title:'Отключить VPN-сессию?',
   content:'Запрос ставится в очередь. Слот останется занят до подписанного подтверждения ElXray.',
   okText:'Запросить отключение',cancelText:'Отмена',okButtonProps:{danger:true},
   onOk:async()=>{
    setBusy(session.id);
    try {
     await elxManagement.drain(selected,session.id);
     messages.info('Отключение запрошено; подтверждение ElXray ожидается.');
     await refresh();
    } catch(e) {messages.error(e instanceof Error?e.message:'Ошибка отключения');}
    finally {setBusy(null);}
   },
  });
 };
 const revoke=(device:Device)=>{
  if (!selected || busy || device.revoked) return;
  modal.confirm({
   title:'Отозвать устройство?',
   content:'Новые сессии будут запрещены. Текущие соединения отключает агент ElXray; занятый слот освобождается только после подписанного подтверждения ядра.',
   okText:'Отозвать устройство',cancelText:'Отмена',okButtonProps:{danger:true},
   onOk:async()=>{
    setBusy(device.id);
    try {
     const result=await elxManagement.revoke(selected,device.id);
     if(result.undrainable>0){
      messages.warning('Устройство отозвано. Не удалось автоматически отключить '+result.undrainable+' старых сессий без доверенной привязки; слоты сохранены.');
     } else {
      messages.info('Устройство отозвано. Ожидают подтверждения ядра: '+result.drainQueued+' сессий.');
     }
     await refresh();
    } catch(e){messages.error(e instanceof Error?e.message:'Ошибка отзыва устройства');}
    finally{setBusy(null);}
   },
  });
 };
 const createSchedule=async()=>{
  if(!selected || !detail.data?.rateBudget)return;
  const thresholdBytes=Math.round(thresholdGB*1_000_000_000);
  const specInput={cadence,timezone,direction,thresholdBytes,
   reducedUp:bitsToBytesPerSecond(reducedUp),reducedDown:bitsToBytesPerSecond(reducedDown)};
  const plan=detail.data.rateBudget;
  if(!Number.isSafeInteger(thresholdBytes)||thresholdBytes<=0||
    specInput.reducedUp<1||specInput.reducedDown<1||
    specInput.reducedUp>plan.up||specInput.reducedDown>plan.down){
    messages.error('Порог должен быть положительным, а сниженная скорость — в пределах приобретённого тарифа.');
    return;
  }
  const fingerprint=JSON.stringify({entitlement:selected,...specInput});
  if(!scheduleAttempt.current||scheduleAttempt.current.fingerprint!==fingerprint){
    scheduleAttempt.current={fingerprint,id:crypto.randomUUID()};
  }
  setBusy('schedule');
  try {
   const result=await elxManagement.createSchedule(selected,{scheduleId:scheduleAttempt.current.id,...specInput});
   messages.success(result.alreadyExisted?'Расписание уже существует':'Расписание создано. Для применения требуются подписанный учёт и активация оператором.');
   scheduleAttempt.current=null;
   setFupOpen(false);
   await refresh();
  }catch(e){messages.error(e instanceof Error?e.message:'Не удалось создать расписание FUP');}
  finally{setBusy(null);}
 };
 const reconcile=async()=>{
  if(!selected)return;
  setBusy('fup');
  try {
   const result=await elxManagement.reconcile(selected);
   messages.info('FUP: запрошено '+result.queued+', ожидается '+result.pending+', без изменений '+result.unchanged);
   await refresh();
  } catch(e){messages.error(e instanceof Error?e.message:'Ошибка FUP');}
  finally {setBusy(null);}
 };
 return <ConfigProvider theme={antdThemeConfig}>
  {contextHolder}{modalHolder}
  <Modal open={fupOpen} title="Создать расписание FUP" okText="Создать" cancelText="Отмена"
   okButtonProps={{loading:busy==='schedule',disabled:busy!==null||!detail.data?.rateBudget}}
   onOk={createSchedule} onCancel={()=>setFupOpen(false)}>
   <Space direction="vertical" style={{width:'100%'}} size="middle">
    <Alert type="info" title="Ограничения тарифа" description="Сниженные скорости не могут быть выше приобретённого бюджета. Для подсчёта трафика нужна привязка подписки к доверенным потокам учёта на нодах."/>
    <Text>Период</Text>
    <Select value={cadence} onChange={(value)=>setCadence(value as 'day'|'week'|'month')}
     options={[{value:'day',label:'День'},{value:'week',label:'Неделя (с понедельника)'},{value:'month',label:'Месяц'}]}/>
    <Text>Часовой пояс IANA</Text>
    <Input value={timezone} onChange={(event)=>setTimezone(event.target.value)} maxLength={128} placeholder="Europe/Moscow"/>
    <Text>Направление трафика</Text>
    <Select value={direction} onChange={(value)=>setDirection(value as 'up'|'down'|'total')}
     options={[{value:'total',label:'Суммарный трафик'},{value:'up',label:'Upload'},{value:'down',label:'Download'}]}/>
    <Text>Порог, ГБ</Text>
    <InputNumber style={{width:'100%'}} min={0.01} step={1} value={thresholdGB}
     onChange={(value)=>setThresholdGB(Number(value??0))}/>
    <Text>После порога: upload, Мбит/с</Text>
    <InputNumber style={{width:'100%'}} min={0.01} step={1} value={reducedUp}
     onChange={(value)=>setReducedUp(Number(value??0))}/>
    <Text>После порога: download, Мбит/с</Text>
    <InputNumber style={{width:'100%'}} min={0.01} step={1} value={reducedDown}
     onChange={(value)=>setReducedDown(Number(value??0))}/>
   </Space>
  </Modal>
  <Layout className="page-shell"><AppSidebar/><Layout className="content-shell">
   <Layout.Content id="content-layout" className="content-area" style={{padding:20}}>
    <Space direction="vertical" size="middle" style={{width:'100%'}}>
     <Space style={{justifyContent:'space-between',width:'100%'}}>
      <Title level={3}>ElX Control</Title>
      <Button onClick={refresh} loading={listing.isFetching||detail.isFetching}>Обновить</Button>
     </Space>
     <Alert type="info" showIcon title="Административное управление" description="Сессия занимает слот даже без передачи трафика. Освобождение требует подтверждения ElXray. Лимиты отображаются как Мбит/с."/>
     {listing.isError&&<Alert type="error" showIcon title="Control недоступен" description={listing.error.message}/>}
     <Card title="Подписки" size="small">
      <Table<Entitlement> rowKey="id" size="small" loading={listing.isLoading} pagination={false} scroll={{x:800}}
       dataSource={listing.data?.items??[]}
       columns={[
        {title:'ID',dataIndex:'id',render:(id:string)=><Text copyable>{id}</Text>},
        {title:'Статус',dataIndex:'enabled',render:(v:boolean)=><Tag color={v?'success':'error'}>{v?'Включена':'Отключена'}</Tag>},
        {title:'Устройств',dataIndex:'devices'},
        {title:'Слотов',render:(_,e)=>occupancy(e)},
        {title:'До',dataIndex:'validUntil',render:(v:string)=>new Date(v).toLocaleString('ru-RU')},
        {title:'',render:(_,e)=><Button type="link" onClick={()=>setSelected(e.id)}>Открыть</Button>},
       ]}/>
      <Space style={{marginTop:12}}>
       <Button disabled={!history.length} onClick={()=>{setCursor(history[history.length-1]);setHistory(h=>h.slice(0,-1));}}>Назад</Button>
       <Button disabled={!listing.data?.nextCursor} onClick={()=>{setHistory(h=>[...h,cursor]);setCursor(listing.data?.nextCursor??'');}}>Далее</Button>
      </Space>
     </Card>
     {selected&&<Card size="small" title="Подписка" extra={<Button onClick={()=>setSelected('')}>Закрыть</Button>}>
      <Spin spinning={detail.isLoading}>
       {detail.isError&&<Alert type="error" showIcon title="Ошибка загрузки" description={detail.error.message}/>}
       {detail.data&&<Space direction="vertical" size="middle" style={{width:'100%'}}>
        <Text copyable>{detail.data.entitlement.id}</Text>
        <Text>Занятые слоты: {occupancy(detail.data.entitlement)}</Text>
        <Text>Общий upload: {detail.data.rateBudget?mbps(detail.data.rateBudget.up):'Не задан'}</Text>
        <Text>Общий download: {detail.data.rateBudget?mbps(detail.data.rateBudget.down):'Не задан'}</Text>
        <Title level={5}>Устройства</Title>
        <Table size="small" rowKey="id" pagination={false} scroll={{x:500}} dataSource={detail.data.devices}
         columns={[
          {title:'ID',dataIndex:'id',render:(id:string)=><Text copyable>{id}</Text>},
          {title:'Поколение ключа',dataIndex:'keyGeneration'},
          {title:'Состояние',dataIndex:'revoked',render:(v:boolean)=><Tag color={v?'error':'success'}>{v?'Отозвано':'Действует'}</Tag>},
          {title:'Действие',render:(_:unknown,d:Device)=><Button size="small" danger disabled={busy!==null||d.revoked}
           loading={busy===d.id} onClick={()=>revoke(d)}>Отозвать</Button>},
         ]}/>
        <Title level={5}>VPN-сессии</Title>
        {detail.data.sessionsTruncated&&<Alert type="warning" title="Отображаются только 200 последних сессий"/>}
        <Table size="small" rowKey="id" pagination={false} scroll={{x:750}} dataSource={detail.data.sessions}
         columns={[
          {title:'ID сессии',dataIndex:'id',render:(id:string)=><Text copyable>{id}</Text>},
          {title:'Устройство',dataIndex:'deviceId'},
          {title:'Нода',dataIndex:'nodeId'},
          {title:'Статус',dataIndex:'state',render:(v:string)=><Tag>{v}</Tag>},
          {title:'',render:(_,s:Session)=><Button size="small" danger loading={busy===s.id}
           disabled={busy!==null||s.state==='draining'||s.state==='released'}
           onClick={()=>drain(s)}>Отключить</Button>},
         ]}/>
        <Title level={5}>Распределение скоростей</Title>
        <Table size="small" rowKey={g=>g.nodeId+g.resourceId} pagination={false} scroll={{x:550}} dataSource={detail.data.grants}
         columns={[
          {title:'Нода',dataIndex:'nodeId'},
          {title:'Upload',dataIndex:'up',render:mbps},
          {title:'Download',dataIndex:'down',render:mbps},
          {title:'Эпоха',dataIndex:'epoch'},
          {title:'Статус',dataIndex:'pending',render:(v:boolean)=><Tag>{v?'Ожидает':'Зафиксирован'}</Tag>},
         ]}/>
        <Space wrap><Title level={5} style={{margin:0}}>FUP</Title>
         <Button onClick={()=>setFupOpen(true)} disabled={busy!==null||!detail.data.rateBudget}>Создать расписание</Button>
         <Button loading={busy==='fup'} disabled={busy!==null} onClick={reconcile}>Согласовать FUP</Button>
        </Space>
        {detail.data.periods.map(p=><Card key={p.id} size="small" title={(p.cadence||'Период')+' · '+p.direction}
          extra={p.scheduleId && p.scheduleEnabled===false?<Tag>Ожидает активации</Tag>:p.fupActive?<Tag color="warning">Снижение активно</Tag>:null}>
          <Text>{new Date(p.startsAt).toLocaleString('ru-RU')} — {new Date(p.endsAt).toLocaleString('ru-RU')}</Text>
          <Progress percent={Number(percentUsed(p).toFixed(1))}/>
          <div>Порог: {(p.thresholdBytes/1_000_000_000).toFixed(2)} ГБ</div>
          <div>После порога: ↑{mbps(p.reducedUp)} / ↓{mbps(p.reducedDown)}</div>
          {p.scheduleId && (() => {
           const coverage = (detail.data.usageCoverage ?? []).find(x => x.scheduleId === p.scheduleId);
           return coverage?.ready
            ? <Alert type="success" showIcon title="Источники подписанного учёта подключены" description="Свежие подписанные показания получены. Отдельно проверьте обязательный lease и применение политики ElXray."/>
            : <Alert type="warning" showIcon title="Учёт трафика ещё не готов" description={coverage
             ? 'Привязано: '+coverage.assignedGrants+' из '+coverage.expectedGrants+', исходный замер: '+coverage.primedGrants+', свежие подписанные показания: '+coverage.freshGrants
             : 'Нет подтверждённых назначений потоков. Нельзя считать FUP действующим.'}/>;
          })()}
         </Card>)}
       </Space>}
      </Spin>
     </Card>}
    </Space>
   </Layout.Content>
  </Layout></Layout>
 </ConfigProvider>;
}
