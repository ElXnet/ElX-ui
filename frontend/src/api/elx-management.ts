import { HttpUtil } from '@/utils';
export interface Entitlement { id:string; activeSlots:number; validUntil:string; enabled:boolean; devices:number; occupied:number }
export interface Device {id:string; keyGeneration:number; revoked:boolean}
export interface Session {id:string; deviceId:string; nodeId:string; profileId?:string; generation:number; state:string; reservedAt:string}
export interface Grant {nodeId:string; resourceId:string; up:number; down:number; epoch:number; pending:boolean}
export interface Period {id:string; startsAt:string; endsAt:string; direction:'up'|'down'|'total'; thresholdBytes:number; upBytes:number; downBytes:number; reducedUp:number; reducedDown:number; fupActive:boolean; cadence?:string; timezone?:string}
export interface Detail {entitlement:Entitlement; devices:Device[]; sessions:Session[]; sessionsTruncated:boolean; rateBudget:{up:number;down:number}|null; grants:Grant[]; periods:Period[]}
export interface Page {items:Entitlement[]; nextCursor?:string}
export interface Reconcile {entitlementId:string; active:boolean; queued:number; pending:number; unchanged:number}
export interface RevokeResult {deviceId:string;revoked:boolean;alreadyRevoked:boolean;drainQueued:number;undrainable:number}
async function request<T>(path:string,post=false):Promise<T> {
 const result=post?await HttpUtil.post<T>('/panel/api/elx'+path,undefined,{silent:true}):await HttpUtil.get<T>('/panel/api/elx'+path,undefined,{silent:true});
 if (!result.success || result.obj==null) throw new Error(result.msg||'Control unavailable');
 return result.obj;
}
export const elxManagement={
 list:(after='')=>request<Page>('/entitlements?limit=50'+(after?'&after='+encodeURIComponent(after):'')),
 get:(id:string)=>request<Detail>('/entitlements/'+encodeURIComponent(id)),
 drain:(id:string,owner:string)=>request<{state:string;release:string}>('/entitlements/'+encodeURIComponent(id)+'/sessions/'+encodeURIComponent(owner)+'/drain',true),
 reconcile:(id:string)=>request<Reconcile>('/entitlements/'+encodeURIComponent(id)+'/reconcile-fup',true),
 revoke:(id:string,device:string)=>request<RevokeResult>('/entitlements/'+encodeURIComponent(id)+'/devices/'+encodeURIComponent(device)+'/revoke',true),
};
export const mbps=(bytesPerSecond:number):string=>(bytesPerSecond*8/1_000_000).toLocaleString('ru-RU',{maximumFractionDigits:2})+' Мбит/с';
export const occupancy=(e:Entitlement):string=>e.occupied+' / '+e.activeSlots;
export function percentUsed(p:Period):number {
 const bytes=p.direction==='up'?p.upBytes:p.direction==='down'?p.downBytes:p.upBytes+p.downBytes;
 return p.thresholdBytes>0?Math.min(100,100*bytes/p.thresholdBytes):0;
}
