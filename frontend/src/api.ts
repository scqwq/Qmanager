export interface Source { id:string;name:string;selected:string;resolved:string;linked:boolean }
export interface SavedSource { id:string;name:string;root:string }
export interface Item { id:string;instance_id:string;relative_path:string;kind:string;source_month:string;role:string;format:string;status:string;bytes:number;fs_modified_at:string;fs_created_at:string|null;preview:boolean;first_seen_at:string;last_seen_at:string;stable_observed_at:string|null;observation_method:string;confidence:string;coverage:string;stability:string }
export interface Bucket {kind:string;month:string;role:string;files:number;bytes:number}
export interface Report { partial:boolean;header_reads:number;reused:number;indexed_updates:number; started_at:string;finished_at:string;complete:boolean;cancelled:boolean;files:number;bytes:number;issues:{path:string;code:string}[];issue_count:number;buckets:Bucket[] }
export interface BackupResult {path:string;files:number;unique_objects:number;excluded:number;bytes:number}
export interface State {index_bytes:number;cached:boolean;last_full_scan:string;reconcile_seconds:number;discovery?:{root:string;linked:boolean;directories:number;skipped:number;complete:boolean;candidates:{root:string;name:string}[];reason:string};phase:string;mode:string;source:Source|null;sources:SavedSource[];scanned:number;bytes:number;report:Report|null;watching:boolean;coverage:string;revision:number;error:string;message:string;backup:BackupResult|null;excluded:number;excluded_items?:{path:string;code:string}[]}
export interface Filter {include_baseline:boolean;kind:string;month:string;role:string;status:string;min_bytes:number;max_bytes:number;offset:number;limit:number;view:string;from:string;to:string}
export interface Page {baseline_files:number;items:Item[];total:number;bytes:number}
export interface Estimate {files:number;bytes:number;excluded:number;estimated_peak:number}
export interface History {at:string;kind:string;detail:string}
interface Bridge {UIReady():Promise<void>;ChooseSource():Promise<string>;StartScan():Promise<void>;CancelScan():Promise<void>;StopWatch():Promise<void>;GetState():Promise<State>;GetPage(f:Filter):Promise<Page>;SwitchSource(id:string):Promise<void>;RenameSource(name:string):Promise<void>;GetEstimate(f:Filter):Promise<Estimate>;CreateBackup(f:Filter):Promise<void>;OpenBackup():Promise<void>;ExportIndex():Promise<string>;GetMediaURL(id:string):Promise<string>;GetHistory(id:string):Promise<History[]>;GetBuckets(view:string):Promise<Bucket[]>;GetFilterBuckets(f:Filter):Promise<Bucket[]>}
declare global {interface Window {go?:{main:{App:Bridge}}}}
export const hasDesktop=()=>Boolean(window.go?.main?.App)
export function api():Bridge {if(!window.go?.main?.App)throw new Error('请使用 Qman 桌面程序打开。');return window.go.main.App}
