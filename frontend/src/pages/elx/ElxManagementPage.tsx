import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, ConfigProvider, Layout, Modal, Progress, Space, Spin, Table, Tag, Typography, message } from 'antd';
import AppSidebar from '@/layouts/AppSidebar';
import { useTheme } from '@/hooks/useTheme';
import { elxManagement, mbps, occupancy, percentUsed } from '@/api/elx-management';
import type { Entitlement, Session, Device } from '@/api/elx-management';
const { Title, Text } = Typography;
export default function ElxManagementPage() {
 const {antdThemeConfig}=useTheme();
 const client=useQueryClient();
 const [selected,setSelected]=useState('');
 const [cursor,setCursor]=useState('');
 const [history,setHistory]=useState<string[]>([]);
 const [busy,setBusy]=useState<string|null>(null);
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
        <Space><Title level={5}>FUP</Title><Button loading={busy==='fup'} disabled={busy!==null} onClick={reconcile}>Согласовать FUP</Button></Space>
        {detail.data.periods.map(p=><Card key={p.id} size="small" title={(p.cadence||'Период')+' · '+p.direction}
          extra={p.fupActive?<Tag color="warning">Снижение активно</Tag>:null}>
          <Text>{new Date(p.startsAt).toLocaleString('ru-RU')} — {new Date(p.endsAt).toLocaleString('ru-RU')}</Text>
          <Progress percent={Number(percentUsed(p).toFixed(1))}/>
          <div>Порог: {(p.thresholdBytes/1_000_000_000).toFixed(2)} ГБ</div>
          <div>После порога: ↑{mbps(p.reducedUp)} / ↓{mbps(p.reducedDown)}</div>
         </Card>)}
       </Space>}
      </Spin>
     </Card>}
    </Space>
   </Layout.Content>
  </Layout></Layout>
 </ConfigProvider>;
}
