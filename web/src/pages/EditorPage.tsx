import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertTriangle, AlignCenter, Eye, Lock, MessageCircle, AlignLeft, AlignRight, ArrowUpDown, BadgeCheck, BarChart3, Bold, Bot, ChevronLeft, Download, Filter, History, Italic, Link2, MessageSquare, MoreHorizontal, Palette, Redo2, Search, Share2, Table2, TableCellsMerge, TableCellsSplit, Underline, Undo2, Workflow, ZoomIn, ZoomOut } from 'lucide-react'
import { AppHeader } from '../components/AppHeader'
import { AIPanel } from '../components/AIPanel'
import { AutomationPanel } from '../components/AutomationPanel'
import { CanvasGrid,type GridMenuCommand,type GridShortcut } from '../components/CanvasGrid'
import { WorkbookMenuBar,type WorkbookMenu } from '../components/WorkbookMenuBar'
import { ShareDialog,accessSummary } from '../components/ShareDialog'
import { ApiError } from '../lib/api'
import { ContextMenu,type MenuItem } from '../components/ContextMenu'
import { ChartDialog } from '../components/ChartDialog'
import { ChartOverlay } from '../components/ChartOverlay'
import { ChartPanel } from '../components/ChartPanel'
import '../components/ChartLauncher.css'
import { CommentPanel } from '../components/CommentPanel'
import { ConflictPanel } from '../components/ConflictPanel'
import { ConditionalFormatDialog } from '../components/ConditionalFormatDialog'
import { DataValidationDialog } from '../components/DataValidationDialog'
import { FilterDialog } from '../components/FilterDialog'
import { FormatDialog,type BorderFormatCommand } from '../components/FormatDialog'
import { LayoutDialog,type LayoutCommand } from '../components/LayoutDialog'
import { NamedRangeDialog } from '../components/NamedRangeDialog'
import { PivotDialog } from '../components/PivotDialog'
import { PivotPanel } from '../components/PivotPanel'
import { PivotResultDialog } from '../components/PivotResultDialog'
import { SheetTabs } from '../components/SheetTabs'
import { SortDialog } from '../components/SortDialog'
import { StructureDialog,type StructureCommand } from '../components/StructureDialog'
import { VersionPanel } from '../components/VersionPanel'
import { WorkbookSearchDialog } from '../components/WorkbookSearchDialog'
import { WorkbookShortcutsDialog } from '../components/WorkbookShortcutsDialog'
import { api, address, newIdempotencyKey } from '../lib/api'
import { collaborationClientId } from '../lib/client'
import { MAX_GRID_COLUMNS, MAX_GRID_ROWS, MAX_PASTE_CELLS } from '../lib/clipboard'
import { cellMerge,mergeStyle as applyMergeStyle,selectedMergedBounds } from '../lib/merge'
import { enqueue, flushOutbox, listOutbox } from '../lib/outbox'
import { materializeSort,type SortOptions } from '../lib/sort'
import { useCollaborationStore } from '../state/collaboration'
import type { ServerEvent } from '../state/collaboration'
import { cellKey, selectedBounds, useEditorStore } from '../state/editor'
import type { ShareRole,AIExecutionResult, AutomationExecutionResult, BuildInfo, Cell, CellConflict, CellConflictResolutionResult, Chart, ConditionalFormat, DataValidation, FilterResult, FilterView, MutationResult, NamedRange, Pivot, PivotData, ReplaceResult, Session, Sheet, SheetLayoutResult, ValidationEvaluation, Workbook, WorkbookSearchMatch } from '../types'

function patchStyle(style:Record<string,unknown>|undefined,patch:Record<string,unknown>){const merged={...(style??{})};for(const [key,value] of Object.entries(patch)){if(value===null)delete merged[key];else merged[key]=value}return merged}
function parseCellAddress(value:string){const match=/^([A-Z]+)([1-9]\d*)$/.exec(value.toUpperCase());if(!match)return;let column=0;for(const character of match[1])column=column*26+character.charCodeAt(0)-64;return{row:Number(match[2]),column}}
function parseNavigationRange(value:string){const parts=value.trim().replaceAll('$','').split(':');if(parts.length<1||parts.length>2)return;const first=parseCellAddress(parts[0]),last=parseCellAddress(parts[1]??parts[0]);if(!first||!last)return;return{startRow:Math.min(first.row,last.row),startColumn:Math.min(first.column,last.column),endRow:Math.max(first.row,last.row),endColumn:Math.max(first.column,last.column)}}
function spillInRange(cells:Map<string,Cell>,range:{startRow:number;startColumn:number;endRow:number;endColumn:number}){for(let row=range.startRow;row<=range.endRow;row+=1)for(let column=range.startColumn;column<=range.endColumn;column+=1){const cell=cells.get(cellKey(row,column));if(cell?.spill_source)return{cell,coordinate:address(row,column)}}}
function editableTarget(target:EventTarget|null){return target instanceof HTMLElement&&Boolean(target.closest('input, textarea, select, [contenteditable="true"]'))}
function gridShortcut(shortcut:GridShortcut){window.dispatchEvent(new CustomEvent<GridShortcut>('kanpic:grid-shortcut',{detail:shortcut}))}
const CLEARABLE_STYLE_KEYS=['bold','italic','underline','strike','color','background','font_size','font_family','horizontal_align','vertical_align','number_format','text_mode','wrap','text_rotation','borders']

export function EditorPage({workbookId,build,session}:{workbookId:string;build?:BuildInfo;session?:Session}) {
  const client=useQueryClient();const workbook=useQuery({queryKey:['workbook',workbookId],queryFn:()=>api<Workbook>(`/api/v1/workbooks/${workbookId}`),retry:(count,error)=>!(error instanceof ApiError&&error.status===403)&&count<2})
  const [activeSheet,setActiveSheet]=useState<Sheet|undefined>();const [serverVersion,setServerVersion]=useState(1);const [rightPanel,setRightPanel]=useState<'ai'|'automation'|'history'|'comments'|'conflicts'|'charts'|'pivots'|null>(()=>new URLSearchParams(window.location.search).has('comment_id')?'comments':'ai'),[searchOpen,setSearchOpen]=useState(false),[shortcutsOpen,setShortcutsOpen]=useState(false),[sortOpen,setSortOpen]=useState(false),[structureOpen,setStructureOpen]=useState(false),[layoutOpen,setLayoutOpen]=useState(false),[formatOpen,setFormatOpen]=useState(false),[filterOpen,setFilterOpen]=useState(false),[validationOpen,setValidationOpen]=useState(false),[conditionalFormatOpen,setConditionalFormatOpen]=useState(false),[namedRangeOpen,setNamedRangeOpen]=useState(false),[chartDialog,setChartDialog]=useState<Chart|null>(),[pivotDialog,setPivotDialog]=useState<Pivot|null>(),[pivotResult,setPivotResult]=useState<Pivot>()
  const [nameBoxValue,setNameBoxValue]=useState('A1'),[pendingNavigation,setPendingNavigation]=useState<{sheetId:string;range:{startRow:number;startColumn:number;endRow:number;endColumn:number}}>()
  const [showFormulas,setShowFormulas]=useState(false),[replaceMode,setReplaceMode]=useState(false),[shareOpen,setShareOpen]=useState(false),[requestingAccess,setRequestingAccess]=useState(false),[accessRequested,setAccessRequested]=useState(false)
  const layoutQueue=useRef<Promise<unknown>>(Promise.resolve()),nameBoxRef=useRef<HTMLInputElement>(null)
  const [overflowMenu,setOverflowMenu]=useState<{x:number;y:number}>()
  const routeNavigation=useRef((()=>{const parameters=new URLSearchParams(window.location.search);return{sheetId:parameters.get('sheet_id')??'',range:parameters.get('range')??'',commentId:parameters.get('comment_id')??''}})()).current,routeNavigationApplied=useRef(false)
  const editor=useEditorStore();const editorSelection=selectedMergedBounds(editor.cells,selectedBounds(editor));const activeCell=editor.cells.get(cellKey(editor.activeRow,editor.activeColumn));const formula=activeCell?.formula||(activeCell?.value==null?'':String(activeCell.value))
  const conflicts=useQuery({queryKey:['cell-conflicts',workbookId,false],queryFn:()=>api<{items:CellConflict[]}>(`/api/v1/workbooks/${workbookId}/conflicts`)})
  const connect=useCollaborationStore(state=>state.connect),disconnect=useCollaborationStore(state=>state.disconnect),sendCursor=useCollaborationStore(state=>state.sendCursor),sendSelection=useCollaborationStore(state=>state.sendSelection)
  const collaborationStatus=useCollaborationStore(state=>state.status),collaborators=useCollaborationStore(state=>state.users)
  const updateVersion=useCallback((value:number)=>setServerVersion(current=>Math.max(current,value)),[])
  const handleCollaborationVersion=useCallback((value:number,event:ServerEvent)=>{updateVersion(value);const data=event.data as {structural?:boolean}|undefined;if(data?.structural&&event.client_id!==collaborationClientId())useEditorStore.getState().reset();client.invalidateQueries({queryKey:['workbook',workbookId]});client.invalidateQueries({queryKey:['cell-conflicts',workbookId]});client.invalidateQueries({queryKey:['data-validations']});client.invalidateQueries({queryKey:['conditional-formats']});client.invalidateQueries({queryKey:['named-ranges',workbookId]});client.invalidateQueries({queryKey:['charts',workbookId]});client.invalidateQueries({queryKey:['pivots',workbookId]});client.invalidateQueries({queryKey:['pivot-data']});client.invalidateQueries({queryKey:['filter-views']});client.invalidateQueries({queryKey:['filter-result']})},[client,updateVersion,workbookId])
  const handleCollaborationEvent=useCallback((event:ServerEvent)=>{if(event.type==='comment.changed'){client.invalidateQueries({queryKey:['comments',workbookId]});client.invalidateQueries({queryKey:['mention-notifications']})}if(event.type==='operation.conflict'){client.invalidateQueries({queryKey:['cell-conflicts',workbookId]});setRightPanel('conflicts')}},[client,workbookId])
  useEffect(()=>{if(workbook.data){setServerVersion(workbook.data.version);setActiveSheet(current=>workbook.data.sheets.find(sheet=>sheet.id===current?.id)??workbook.data.sheets[0])}},[workbook.data])
  useEffect(()=>{if(routeNavigationApplied.current||!workbook.data)return;routeNavigationApplied.current=true;const sheet=workbook.data.sheets.find(candidate=>candidate.id===routeNavigation.sheetId);if(!sheet)return;const target=parseNavigationRange(routeNavigation.range);if(target)setPendingNavigation({sheetId:sheet.id,range:target});setActiveSheet(sheet)},[routeNavigation,workbook.data])
  useEffect(()=>{editor.reset();if(pendingNavigation&&pendingNavigation.sheetId===activeSheet?.id){const target=pendingNavigation.range;editor.select(target.startRow,target.startColumn);editor.select(target.endRow,target.endColumn,true);setPendingNavigation(undefined)}},[activeSheet?.id])
  const selectionAddress=editorSelection.startRow===editorSelection.endRow&&editorSelection.startColumn===editorSelection.endColumn?address(editor.activeRow,editor.activeColumn):`${address(editorSelection.startRow,editorSelection.startColumn)}:${address(editorSelection.endRow,editorSelection.endColumn)}`
  // Keep whatever the user is typing: only mirror the selection into the name
  // box when it does not have focus.
  useEffect(()=>{if(document.activeElement!==nameBoxRef.current)setNameBoxValue(selectionAddress)},[selectionAddress])
  useEffect(()=>{connect(workbookId,handleCollaborationVersion,handleCollaborationEvent);return()=>disconnect()},[connect,disconnect,handleCollaborationEvent,handleCollaborationVersion,workbookId])
  useEffect(()=>{if(activeSheet&&collaborationStatus==='connected'){sendCursor({sheet_id:activeSheet.id,row:editor.activeRow,column:editor.activeColumn});sendSelection({sheet_id:activeSheet.id,start:{row:editorSelection.startRow,column:editorSelection.startColumn},end:{row:editorSelection.endRow,column:editorSelection.endColumn}})}},[activeSheet,collaborationStatus,sendCursor,sendSelection])
  useEffect(()=>{if(editor.conflicts>0)client.invalidateQueries({queryKey:['cell-conflicts',workbookId]})},[client,editor.conflicts,workbookId])
  useEffect(()=>{if(!conflicts.data)return;const current=useEditorStore.getState(),count=conflicts.data.items.length;if((current.saveState==='saved'||current.saveState==='conflict')&&(current.conflicts!==count||current.saveState!==(count>0?'conflict':'saved')))current.setSaveState(count>0?'conflict':'saved',count)},[conflicts.data])
  const nextSheetName=()=>{const used=new Set((workbook.data?.sheets??[]).map(sheet=>sheet.name.toLowerCase()));for(let index=1;;index++){const name=`Sheet${index}`;if(!used.has(name.toLowerCase()))return name}}
  const filterViews=useQuery({queryKey:['filter-views',activeSheet?.id],queryFn:()=>api<{items:FilterView[]}>(`/api/v1/sheets/${activeSheet!.id}/filter-views`),enabled:Boolean(activeSheet)})
  const activeFilter=filterViews.data?.items.find(view=>view.active)
  const filterResult=useQuery({queryKey:['filter-result',activeFilter?.id,activeFilter?.updated_at,serverVersion],queryFn:()=>api<FilterResult>(`/api/v1/filter-views/${activeFilter!.id}:evaluate`,{method:'POST',body:'{}'}),enabled:Boolean(activeFilter)})
  const refreshFilters=async()=>{await client.invalidateQueries({queryKey:['filter-views',activeSheet?.id]});await client.invalidateQueries({queryKey:['filter-result']})}
  const createFilter=async(input:{name:string;range:string;header_rows:number;criteria:unknown[];active:boolean})=>{const item=await api<FilterView>(`/api/v1/sheets/${activeSheet!.id}/filter-views`,{method:'POST',body:JSON.stringify({...input,idempotency_key:newIdempotencyKey()})});await refreshFilters();return item}
  const updateFilter=async(id:string,input:Record<string,unknown>)=>{const item=await api<FilterView>(`/api/v1/filter-views/${id}`,{method:'PATCH',body:JSON.stringify(input)});await refreshFilters();return item}
  const deleteFilter=async(id:string)=>{await api(`/api/v1/filter-views/${id}`,{method:'DELETE'});await refreshFilters()}
  const validations=useQuery({queryKey:['data-validations',activeSheet?.id],queryFn:()=>api<{items:DataValidation[]}>(`/api/v1/sheets/${activeSheet!.id}/data-validations`),enabled:Boolean(activeSheet)})
  const refreshValidations=async()=>{await client.invalidateQueries({queryKey:['data-validations',activeSheet?.id]});await client.invalidateQueries({queryKey:['workbook',workbookId]})}
  const createValidation=async(input:Record<string,unknown>)=>{const item=await api<DataValidation>(`/api/v1/sheets/${activeSheet!.id}/data-validations`,{method:'POST',body:JSON.stringify({...input,idempotency_key:newIdempotencyKey()})});updateVersion(item.workbook_version);await refreshValidations();return item}
  const updateValidation=async(id:string,input:Record<string,unknown>)=>{const item=await api<DataValidation>(`/api/v1/data-validations/${id}`,{method:'PATCH',body:JSON.stringify(input)});updateVersion(item.workbook_version);await refreshValidations();return item}
  const deleteValidation=async(rule:DataValidation)=>{await api(`/api/v1/data-validations/${rule.id}?expected_revision=${rule.revision}`,{method:'DELETE'});await refreshValidations();const latest=await api<Workbook>(`/api/v1/workbooks/${workbookId}`);updateVersion(latest.version)}
  const evaluateValidation=(id:string)=>api<ValidationEvaluation>(`/api/v1/data-validations/${id}:evaluate`,{method:'POST',body:'{}'})
  const conditionalFormats=useQuery({queryKey:['conditional-formats',activeSheet?.id],queryFn:()=>api<{items:ConditionalFormat[]}>(`/api/v1/sheets/${activeSheet!.id}/conditional-formats`),enabled:Boolean(activeSheet)})
  const refreshConditionalFormats=async()=>{await client.invalidateQueries({queryKey:['conditional-formats',activeSheet?.id]});await client.invalidateQueries({queryKey:['workbook',workbookId]})}
  const createConditionalFormat=async(input:Record<string,unknown>)=>{const idempotencyKey=newIdempotencyKey();const item=await api<ConditionalFormat>(`/api/v1/sheets/${activeSheet!.id}/conditional-formats`,{method:'POST',headers:{'Idempotency-Key':idempotencyKey},body:JSON.stringify({...input,idempotency_key:idempotencyKey})});updateVersion(item.workbook_version);await refreshConditionalFormats();return item}
  const updateConditionalFormat=async(id:string,input:Record<string,unknown>)=>{const item=await api<ConditionalFormat>(`/api/v1/conditional-formats/${id}`,{method:'PATCH',body:JSON.stringify(input)});updateVersion(item.workbook_version);await refreshConditionalFormats();return item}
  const deleteConditionalFormat=async(rule:ConditionalFormat)=>{await api(`/api/v1/conditional-formats/${rule.id}?expected_revision=${rule.revision}`,{method:'DELETE'});await refreshConditionalFormats();const latest=await api<Workbook>(`/api/v1/workbooks/${workbookId}`);updateVersion(latest.version)}
  const namedRanges=useQuery({queryKey:['named-ranges',workbookId],queryFn:()=>api<{items:NamedRange[]}>(`/api/v1/workbooks/${workbookId}/named-ranges`)})
	const charts=useQuery({queryKey:['charts',workbookId,activeSheet?.id],queryFn:()=>api<{items:Chart[]}>(`/api/v1/workbooks/${workbookId}/charts?sheet_id=${activeSheet!.id}`),enabled:Boolean(activeSheet)})
	const pivots=useQuery({queryKey:['pivots',workbookId,activeSheet?.id],queryFn:()=>api<{items:Pivot[]}>(`/api/v1/workbooks/${workbookId}/pivots?sheet_id=${activeSheet!.id}`),enabled:Boolean(activeSheet)})
  const refreshNamedRanges=async()=>{await client.invalidateQueries({queryKey:['named-ranges',workbookId]});await client.invalidateQueries({queryKey:['workbook',workbookId]})}
  const createNamedRange=async(input:Record<string,unknown>)=>{const item=await api<NamedRange>(`/api/v1/workbooks/${workbookId}/named-ranges`,{method:'POST',body:JSON.stringify({...input,idempotency_key:newIdempotencyKey()})});updateVersion(item.workbook_version);await refreshNamedRanges();return item}
  const updateNamedRange=async(id:string,input:Record<string,unknown>)=>{const item=await api<NamedRange>(`/api/v1/named-ranges/${id}`,{method:'PATCH',body:JSON.stringify(input)});updateVersion(item.workbook_version);await refreshNamedRanges();return item}
  const deleteNamedRange=async(item:NamedRange)=>{await api(`/api/v1/named-ranges/${item.id}?expected_revision=${item.revision}`,{method:'DELETE'});await refreshNamedRanges();const latest=await api<Workbook>(`/api/v1/workbooks/${workbookId}`);updateVersion(latest.version)}
	const refreshCharts=async()=>{await client.invalidateQueries({queryKey:['charts',workbookId]});await client.invalidateQueries({queryKey:['workbook',workbookId]})}
	const createChart=async(input:Record<string,unknown>)=>{const idempotencyKey=newIdempotencyKey();const item=await api<Chart>(`/api/v1/workbooks/${workbookId}/charts`,{method:'POST',headers:{'Idempotency-Key':idempotencyKey},body:JSON.stringify({...input,idempotency_key:idempotencyKey})});updateVersion(item.workbook_version);await refreshCharts();return item}
	const updateChart=async(item:Chart,input:Record<string,unknown>)=>{const updated=await api<Chart>(`/api/v1/charts/${item.id}`,{method:'PATCH',body:JSON.stringify(input)});updateVersion(updated.workbook_version);await refreshCharts();return updated}
	const deleteChart=async(item:Chart)=>{await api(`/api/v1/charts/${item.id}?expected_revision=${item.revision}`,{method:'DELETE'});await refreshCharts();const latest=await api<Workbook>(`/api/v1/workbooks/${workbookId}`);updateVersion(latest.version)}
	const refreshPivots=async()=>{await client.invalidateQueries({queryKey:['pivots',workbookId]});await client.invalidateQueries({queryKey:['pivot-data']});await client.invalidateQueries({queryKey:['workbook',workbookId]})}
	const createPivot=async(input:Record<string,unknown>)=>{const idempotencyKey=newIdempotencyKey();const item=await api<Pivot>(`/api/v1/workbooks/${workbookId}/pivots`,{method:'POST',headers:{'Idempotency-Key':idempotencyKey},body:JSON.stringify({...input,idempotency_key:idempotencyKey})});updateVersion(item.workbook_version);await refreshPivots();return item}
	const updatePivot=async(item:Pivot,input:Record<string,unknown>)=>{const updated=await api<Pivot>(`/api/v1/pivots/${item.id}`,{method:'PATCH',body:JSON.stringify(input)});updateVersion(updated.workbook_version);await refreshPivots();return updated}
	const deletePivot=async(item:Pivot)=>{await api(`/api/v1/pivots/${item.id}?expected_revision=${item.revision}`,{method:'DELETE'});await refreshPivots();const latest=await api<Workbook>(`/api/v1/workbooks/${workbookId}`);updateVersion(latest.version)}
	const refreshPivot=async(item:Pivot)=>{await api<PivotData>(`/api/v1/pivots/${item.id}/refresh`,{method:'POST',body:'{}'});await refreshPivots()}
  const navigateToRange=(sheetId:string,value:string)=>{const target=parseNavigationRange(value),sheet=workbook.data?.sheets.find(candidate=>candidate.id===sheetId);if(!target||!sheet)return false;if(activeSheet?.id===sheetId){editor.select(target.startRow,target.startColumn);editor.select(target.endRow,target.endColumn,true)}else{setPendingNavigation({sheetId,range:target});setActiveSheet(sheet)}return true}
  // After a successful jump the name box shows the resolved range and hands
  // focus back to the grid, so the typed name is never left stale.
  const submitNameBox=()=>{
    const value=nameBoxValue.trim(),named=(namedRanges.data?.items??[]).find(item=>item.name.toLowerCase()===value.toLowerCase())
    const target=named?{sheetId:named.sheet_id,reference:named.range}:activeSheet?{sheetId:activeSheet.id,reference:value}:undefined
    const parsed=target?parseNavigationRange(target.reference):undefined
    if(!target||!parsed||!navigateToRange(target.sheetId,target.reference)){setNameBoxValue(selectionAddress);return}
    setNameBoxValue(parsed.startRow===parsed.endRow&&parsed.startColumn===parsed.endColumn?address(parsed.startRow,parsed.startColumn):`${address(parsed.startRow,parsed.startColumn)}:${address(parsed.endRow,parsed.endColumn)}`)
    nameBoxRef.current?.blur()
  }
  const refreshWorkbook=async()=>client.invalidateQueries({queryKey:['workbook',workbookId]})
  const createSheet=async()=>{const sheet=await api<Sheet>(`/api/v1/workbooks/${workbookId}/sheets`,{method:'POST',body:JSON.stringify({name:nextSheetName()})});setActiveSheet(sheet);await refreshWorkbook()}
  const updateSheet=async(sheet:Sheet,input:Record<string,unknown>)=>{const updated=await api<Sheet>(`/api/v1/sheets/${sheet.id}`,{method:'PATCH',body:JSON.stringify(input)});if(activeSheet?.id===sheet.id)setActiveSheet(updated);await refreshWorkbook()}
  const duplicateSheet=async(sheet:Sheet)=>{const duplicated=await api<Sheet>(`/api/v1/sheets/${sheet.id}/duplicate`,{method:'POST',body:'{}'});setActiveSheet(duplicated);await refreshWorkbook()}
  const deleteSheet=async(sheet:Sheet)=>{const ordered=workbook.data!.sheets;const index=ordered.findIndex(item=>item.id===sheet.id);const fallback=ordered[index===0?1:index-1];await api(`/api/v1/sheets/${sheet.id}`,{method:'DELETE'});if(activeSheet?.id===sheet.id&&fallback)setActiveSheet(fallback);await refreshWorkbook()}
  const revertOperation=async(mode:'undo'|'redo')=>{if(!writable())return;if(!navigator.onLine){alert('Undo와 Redo는 서버에 다시 연결한 후 사용할 수 있습니다.');return}const target=mode==='undo'?editor.takeUndo():editor.takeRedo();if(!target)return;editor.setSaveState('saving');try{const result=await api<MutationResult>(`/api/v1/operations/${target}:undo`,{method:'POST',body:JSON.stringify({idempotency_key:`undo:${target}`,client_id:collaborationClientId()})});updateVersion(result.server_version);if(result.applied_cells>0){if(mode==='undo')editor.completeUndo(result.operation_id);else editor.completeRedo(result.operation_id)}else{if(mode==='undo')editor.restoreUndo(target);else editor.restoreRedo(target)}editor.setSaveState(result.conflicts.length?'conflict':'saved',result.conflicts.length)}catch{if(mode==='undo')editor.restoreUndo(target);else editor.restoreRedo(target);editor.setSaveState('error')}}
  const denyWrite=()=>{editor.setSaveState('error');alert('보기 전용 권한입니다. 소유자에게 편집 권한을 요청하세요.')}
  const writable=()=>{const role=workbook.data?.access_role??'owner';if(role==='editor'||role==='owner')return true;denyWrite();return false}
  const applyFormat=async(patch:Record<string,unknown>,border?:BorderFormatCommand)=>{if(!activeSheet||!writable())return;const rows=editorSelection.endRow-editorSelection.startRow+1,columns=editorSelection.endColumn-editorSelection.startColumn+1;if(rows*columns>MAX_PASTE_CELLS){alert(`서식 적용은 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`);return}const updatedAt=new Date().toISOString(),optimistic:Cell[]=[];for(let row=editorSelection.startRow;row<=editorSelection.endRow;row+=1)for(let column=editorSelection.startColumn;column<=editorSelection.endColumn;column+=1){const current=editor.cells.get(cellKey(row,column));optimistic.push({sheet_id:activeSheet.id,row,column,value:current?.value,formula:current?.formula,spill_source:current?.spill_source,style:patchStyle(current?.style,patch),updated_at:updatedAt})}editor.putCells(optimistic);editor.setSaveState(navigator.onLine?'saving':'offline');const id=newIdempotencyKey();await enqueue({id,sheetId:activeSheet.id,endpoint:'format',attempts:0,createdAt:Date.now(),body:{base_version:serverVersion,idempotency_key:id,client_id:collaborationClientId(),range:`${address(editorSelection.startRow,editorSelection.startColumn)}:${address(editorSelection.endRow,editorSelection.endColumn)}`,style:patch,...(border?{border}:{})}});await flushOutbox((_operation,result)=>{const applied=result as MutationResult;updateVersion(applied.server_version);if(!applied.duplicate&&applied.applied_cells>0)editor.recordOperation(applied.operation_id);editor.setSaveState(applied.conflicts?.length?'conflict':'saved',applied.conflicts?.length||0)})}
  const changeMerge=async(merge:boolean)=>{if(!activeSheet||!writable())return;const rows=editorSelection.endRow-editorSelection.startRow+1,columns=editorSelection.endColumn-editorSelection.startColumn+1;if(merge&&rows*columns<2){alert('두 개 이상의 셀을 선택해 병합하세요.');return}if(rows*columns>MAX_PASTE_CELLS){alert(`셀 병합은 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`);return}const spill=spillInRange(editor.cells,editorSelection);if(spill){alert(`${spill.coordinate}은(는) ${spill.cell.spill_source} 배열 수식의 결과이므로 병합할 수 없습니다.`);return}if(merge){for(let row=editorSelection.startRow;row<=editorSelection.endRow;row+=1)for(let column=editorSelection.startColumn;column<=editorSelection.endColumn;column+=1)if(cellMerge(editor.cells.get(cellKey(row,column)))){alert('선택 범위가 기존 병합 셀과 겹칩니다. 먼저 병합을 해제하세요.');return}}const updatedAt=new Date().toISOString(),optimistic:Cell[]=[];for(let row=editorSelection.startRow;row<=editorSelection.endRow;row+=1)for(let column=editorSelection.startColumn;column<=editorSelection.endColumn;column+=1){const current=editor.cells.get(cellKey(row,column));optimistic.push({sheet_id:activeSheet.id,row,column,value:current?.value,formula:current?.formula,spill_source:current?.spill_source,style:applyMergeStyle(current?.style,editorSelection,merge),updated_at:updatedAt})}editor.putCells(optimistic);editor.setSaveState(navigator.onLine?'saving':'offline');const id=newIdempotencyKey(),endpoint=merge?'merge':'unmerge';await enqueue({id,sheetId:activeSheet.id,endpoint,attempts:0,createdAt:Date.now(),body:{base_version:serverVersion,idempotency_key:id,client_id:collaborationClientId(),range:`${address(editorSelection.startRow,editorSelection.startColumn)}:${address(editorSelection.endRow,editorSelection.endColumn)}`}});await flushOutbox((_operation,result)=>{const applied=result as MutationResult;updateVersion(applied.server_version);if(!applied.duplicate&&applied.applied_cells>0)editor.recordOperation(applied.operation_id);editor.setSaveState(applied.conflicts?.length?'conflict':'saved',applied.conflicts?.length||0)})}
  const sortSelection=async(options:SortOptions)=>{if(!activeSheet||!writable())return;const spill=spillInRange(editor.cells,editorSelection);if(spill){const error=new Error(`${spill.coordinate}은(는) ${spill.cell.spill_source} 배열 수식의 결과이므로 정렬할 수 없습니다.`);alert(error.message);throw error}let optimistic:Cell[];try{optimistic=materializeSort(editor.cells,editorSelection,options,activeSheet.id)}catch(error){alert(error instanceof Error?error.message:'범위를 정렬하지 못했습니다.');throw error}editor.putCells(optimistic);editor.setSaveState(navigator.onLine?'saving':'offline');const id=newIdempotencyKey();await enqueue({id,sheetId:activeSheet.id,endpoint:'sort',attempts:0,createdAt:Date.now(),body:{base_version:serverVersion,idempotency_key:id,client_id:collaborationClientId(),range:`${address(editorSelection.startRow,editorSelection.startColumn)}:${address(editorSelection.endRow,editorSelection.endColumn)}`,keys:options.keys,header_rows:options.headerRows,case_sensitive:options.caseSensitive}});await flushOutbox((_operation,result)=>{const applied=result as MutationResult;updateVersion(applied.server_version);if(!applied.duplicate&&applied.applied_cells>0)editor.recordOperation(applied.operation_id);editor.setSaveState(applied.conflicts?.length?'conflict':'saved',applied.conflicts?.length||0)})}
  const applyStructure=async(command:StructureCommand)=>{if(!activeSheet||!writable())return;if(!navigator.onLine){alert('행과 열 구조 변경은 서버에 연결된 상태에서만 사용할 수 있습니다.');throw new Error('offline')}editor.setSaveState('saving');try{await flushOutbox((_operation,result)=>{const applied=result as MutationResult;updateVersion(applied.server_version)});if((await listOutbox()).length>0)throw new Error('저장 대기 중인 변경을 먼저 서버에 반영해야 합니다.');const latest=await api<Workbook>(`/api/v1/workbooks/${workbookId}`),idempotencyKey=newIdempotencyKey();const result=await api<MutationResult>(`/api/v1/sheets/${activeSheet.id}/structure:apply`,{method:'PATCH',headers:{'Idempotency-Key':idempotencyKey},body:JSON.stringify({...command,base_version:latest.version,idempotency_key:idempotencyKey,client_id:collaborationClientId()})});const targetRow=Math.max(1,command.axis==='row'?command.index:editorSelection.startRow),targetColumn=Math.max(1,command.axis==='column'?command.index:editorSelection.startColumn);editor.reset();editor.select(targetRow,targetColumn);updateVersion(result.server_version);editor.setSaveState('saved');await Promise.all([client.invalidateQueries({queryKey:['workbook',workbookId]}),client.invalidateQueries({queryKey:['data-validations']}),client.invalidateQueries({queryKey:['conditional-formats']}),client.invalidateQueries({queryKey:['named-ranges',workbookId]}),client.invalidateQueries({queryKey:['charts',workbookId]}),client.invalidateQueries({queryKey:['pivots',workbookId]}),client.invalidateQueries({queryKey:['pivot-data']}),client.invalidateQueries({queryKey:['filter-views']}),client.invalidateQueries({queryKey:['filter-result']})])}catch(error){editor.setSaveState('error');const message=error instanceof Error?error.message:'행 또는 열을 변경하지 못했습니다.';if(message!=='offline')alert(message);throw error}}
  const applySheetLayout=async(command:LayoutCommand)=>{if(!activeSheet||!writable())return;if(!navigator.onLine){alert('시트 레이아웃은 서버에 연결된 상태에서 변경할 수 있습니다.');throw new Error('offline')}editor.setSaveState('saving');try{await flushOutbox((_operation,result)=>{const applied=result as MutationResult;updateVersion(applied.server_version)});if((await listOutbox()).length>0)throw new Error('저장 대기 중인 변경을 먼저 서버에 반영해야 합니다.');const latest=await api<Workbook>(`/api/v1/workbooks/${workbookId}`),sheet=latest.sheets.find(item=>item.id===activeSheet.id);if(!sheet)throw new Error('시트를 찾을 수 없습니다.');const idempotencyKey=newIdempotencyKey();const result=await api<SheetLayoutResult>(`/api/v1/sheets/${activeSheet.id}/layout:apply`,{method:'PATCH',headers:{'Idempotency-Key':idempotencyKey},body:JSON.stringify({...command,expected_revision:sheet.layout?.revision??1,idempotency_key:idempotencyKey,client_id:collaborationClientId()})});setActiveSheet(current=>current?.id===result.sheet_id?{...current,layout:result.layout}:current);updateVersion(result.server_version);editor.setSaveState('saved');await client.invalidateQueries({queryKey:['workbook',workbookId]})}catch(error){editor.setSaveState('error');const message=error instanceof Error?error.message:'시트 레이아웃을 변경하지 못했습니다.';if(message!=='offline')alert(message);throw error}}
  // Header drags, auto-fit and menu commands can fire back to back, so layout
  // mutations run one at a time and never race on the sheet layout revision.
  const applyLayout=(command:LayoutCommand)=>{
    const next=layoutQueue.current.catch(()=>{}).then(()=>applySheetLayout(command))
    layoutQueue.current=next
    return next
  }
  const exportWorkbook=async(format:'xlsx'|'csv')=>{const response=await fetch('/api/v1/exports',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({workbook_id:workbookId,sheet_id:activeSheet?.id,format})});if(!response.ok)return alert('파일을 내보내지 못했습니다.');const blob=await response.blob();const disposition=response.headers.get('Content-Disposition')||'';const encoded=disposition.match(/filename\*=UTF-8''([^;]+)/)?.[1];const basic=disposition.match(/filename="?([^";]+)"?/)?.[1];const name=encoded?decodeURIComponent(encoded):basic||`kanpic.${format}`;const link=document.createElement('a');link.href=URL.createObjectURL(blob);link.download=name;link.click();URL.revokeObjectURL(link.href)}
  const handleRestored=async(result:MutationResult)=>{editor.reset();updateVersion(result.server_version);await Promise.all([client.invalidateQueries({queryKey:['workbook',workbookId]}),client.invalidateQueries({queryKey:['conditional-formats']}),client.invalidateQueries({queryKey:['data-validations']}),client.invalidateQueries({queryKey:['named-ranges',workbookId]}),client.invalidateQueries({queryKey:['charts',workbookId]}),client.invalidateQueries({queryKey:['pivots',workbookId]})])}
  const handleConflictResolved=(result:CellConflictResolutionResult)=>{updateVersion(result.operation.server_version);if(!result.operation.duplicate&&result.operation.applied_cells>0)editor.recordOperation(result.operation.operation_id);editor.setSaveState('saved');client.invalidateQueries({queryKey:['workbook',workbookId]})}
  const handleAIExecuted=(result:AIExecutionResult)=>{updateVersion(result.operation.server_version);editor.reset();editor.setSaveState('saved');client.invalidateQueries({queryKey:['workbook',workbookId]});client.invalidateQueries({queryKey:['ai-actions',workbookId]})}
  const handleAutomationExecuted=(result:AutomationExecutionResult)=>{updateVersion(result.operation.server_version);editor.reset();editor.setSaveState('saved');client.invalidateQueries({queryKey:['workbook',workbookId]});client.invalidateQueries({queryKey:['automations',workbookId]})}
  const clearFormat=()=>applyFormat(Object.fromEntries(CLEARABLE_STYLE_KEYS.map(key=>[key,null])))
  const hideSelection=(axis:'row'|'column')=>applyLayout(axis==='row'
    ?{action:'hide',axis,start:editorSelection.startRow,count:editorSelection.endRow-editorSelection.startRow+1}
    :{action:'hide',axis,start:editorSelection.startColumn,count:editorSelection.endColumn-editorSelection.startColumn+1})
  const freezeToSelection=()=>applyLayout({action:'freeze',frozen_rows:Math.max(0,Math.min(100,editorSelection.startRow-1)),frozen_columns:Math.max(0,Math.min(50,editorSelection.startColumn-1))})
  // Whole-row and whole-column selections insert or delete directly; anything
  // else opens the dialog so the axis is chosen explicitly.
  const changeStructure=async(action:'insert'|'delete')=>{
    const wholeRows=editorSelection.startColumn<=1&&editorSelection.endColumn>=MAX_GRID_COLUMNS
    const wholeColumns=editorSelection.startRow<=1&&editorSelection.endRow>=MAX_GRID_ROWS
    if(!wholeRows&&!wholeColumns){setStructureOpen(true);return}
    const axis=wholeRows?'row':'column'
    const index=axis==='row'?editorSelection.startRow:editorSelection.startColumn
    const count=axis==='row'?editorSelection.endRow-editorSelection.startRow+1:editorSelection.endColumn-editorSelection.startColumn+1
    if(action==='delete'&&!window.confirm(`선택한 ${count}개 ${axis==='row'?'행':'열'}을 삭제할까요? 삭제 전 복구 버전이 자동 생성됩니다.`))return
    await applyStructure({axis,action,index,count})
  }
  const moveSheet=(delta:number)=>{const sheets=workbook.data?.sheets??[];if(sheets.length<2||!activeSheet)return;const index=sheets.findIndex(sheet=>sheet.id===activeSheet.id);setActiveSheet(sheets[(index+delta+sheets.length)%sheets.length])}
  const openSearch=(replace:boolean)=>{setReplaceMode(replace);setSearchOpen(true)}
  const copySelectionLink=async()=>{
    const parameters=new URLSearchParams({sheet_id:activeSheet?.id??'',range:selectionAddress})
    const link=`${window.location.origin}/workbooks/${workbookId}?${parameters.toString()}`
    try{await navigator.clipboard?.writeText(link)}catch{window.prompt('셀 링크를 복사하세요.',link)}
  }
  const handleReplaced=async(result:ReplaceResult)=>{updateVersion(result.server_version);editor.reset();editor.setSaveState('saved');await client.invalidateQueries({queryKey:['workbook',workbookId]})}
  // Sorting a whole column keeps the surrounding data block together, so the
  // computed region is confirmed before the existing range sort runs.
  const sortRegion=async(command:Extract<GridMenuCommand,{command:'sort-region'}>)=>{
    const {region,column,direction,headerRows}=command
    const label=`${address(region.startRow,region.startColumn)}:${address(region.endRow,region.endColumn)}`
    if(!window.confirm(`${label} 범위를 ${column}열 기준 ${direction==='asc'?'오름차순':'내림차순'}으로 정렬할까요?${headerRows?' 첫 행은 머리글로 유지합니다.':''}`))return
    editor.select(region.startRow,region.startColumn);editor.select(region.endRow,region.endColumn,true)
    await sortSelection({keys:[{column,direction}],headerRows,caseSensitive:false})
  }
  const handleGridMenu=(command:GridMenuCommand)=>{
    switch(command.command){
      case 'sort-dialog':setSortOpen(true);return
      case 'sort-region':void sortRegion(command);return
      case 'filter':setFilterOpen(true);return
      case 'comment':setRightPanel('comments');return
      case 'named-range':setNamedRangeOpen(true);return
      case 'conditional-format':setConditionalFormatOpen(true);return
      case 'data-validation':setValidationOpen(true);return
      case 'chart':setChartDialog(null);return
      case 'pivot':setPivotDialog(null);return
      case 'format-dialog':setFormatOpen(true);return
      case 'layout-dialog':setLayoutOpen(true);return
      case 'structure-dialog':setStructureOpen(true);return
      case 'clear-format':void clearFormat();return
      case 'find-replace':openSearch(true);return
      case 'merge':void changeMerge(command.merge);return
    }
  }
  const saveWorkbook=async()=>{if(!navigator.onLine){editor.setSaveState('offline');return}editor.setSaveState('saving');try{await flushOutbox((_operation,result)=>{const applied=result as MutationResult;updateVersion(applied.server_version);if(!applied.duplicate&&applied.applied_cells>0)editor.recordOperation(applied.operation_id);editor.setSaveState(applied.conflicts?.length?'conflict':'saved',applied.conflicts?.length||0)});const current=useEditorStore.getState();if(current.saveState==='saving')current.setSaveState(current.conflicts?'conflict':'saved',current.conflicts)}catch{editor.setSaveState('error')}}
  useEffect(()=>{const shortcut=(event:KeyboardEvent)=>{if(event.defaultPrevented||editableTarget(event.target)||document.querySelector('[role="dialog"][aria-modal="true"]'))return;const primary=event.ctrlKey||event.metaKey,key=event.key.toLowerCase(),numberFormats:Record<string,string>={Digit1:'#,##0.00',Digit2:'hh:mm:ss',Digit3:'yyyy-mm-dd',Digit4:'₩#,##0',Digit5:'0.00%',Digit6:'0.00E+00'}
    if(primary&&event.altKey&&key==='m'){event.preventDefault();setRightPanel('comments');return}
    if(primary&&event.code==='Slash'){event.preventDefault();setShortcutsOpen(true);return}
    if(primary&&key==='h'){event.preventDefault();openSearch(true);return}
    if(primary&&(key==='f'||key==='k')){event.preventDefault();openSearch(false);return}
    if(primary&&event.code==='Backquote'){event.preventDefault();setShowFormulas(current=>!current);return}
    if(primary&&event.code==='Backslash'){event.preventDefault();void clearFormat();return}
    if(primary&&event.altKey&&(event.code==='Equal'||event.code==='NumpadAdd')){event.preventDefault();void changeStructure('insert');return}
    if(primary&&event.altKey&&(event.code==='Minus'||event.code==='NumpadSubtract')){event.preventDefault();void changeStructure('delete');return}
    if(event.altKey&&!primary&&(event.code==='Equal'||event.code==='NumpadAdd')){event.preventDefault();gridShortcut({command:'auto-sum'});return}
    if(primary&&!event.shiftKey&&event.code==='Semicolon'){event.preventDefault();gridShortcut({command:'insert-today'});return}
    if(primary&&event.shiftKey&&event.code==='Semicolon'){event.preventDefault();gridShortcut({command:'insert-now'});return}
    if(primary&&event.altKey&&event.code==='Digit0'){event.preventDefault();void hideSelection('column');return}
    if(primary&&event.altKey&&event.code==='Digit9'){event.preventDefault();void hideSelection('row');return}
    if(primary&&event.shiftKey&&event.code==='Digit0'){event.preventDefault();void applyLayout({action:'show_all',axis:'column'});return}
    if(primary&&event.shiftKey&&event.code==='Digit9'){event.preventDefault();void applyLayout({action:'show_all',axis:'row'});return}
    if(primary&&event.key==='PageDown'){event.preventDefault();moveSheet(1);return}
    if(primary&&event.key==='PageUp'){event.preventDefault();moveSheet(-1);return}
    if(event.key==='PageDown'||event.key==='PageUp'){event.preventDefault();gridShortcut({command:'move-page',direction:event.altKey?event.key==='PageDown'?'right':'left':event.key==='PageDown'?'down':'up',extend:event.shiftKey});return}
    if(primary&&event.shiftKey&&key==='a'){event.preventDefault();gridShortcut({command:'select-data-region'});return}
    if(primary&&key==='s'){event.preventDefault();void saveWorkbook();return}
    if(primary&&key==='z'){event.preventDefault();void revertOperation(event.shiftKey?'redo':'undo');return}
    if(primary&&key==='y'){event.preventDefault();void revertOperation('redo');return}
    if(event.shiftKey&&event.key==='F11'){event.preventDefault();void createSheet();return}
    if(primary&&event.shiftKey&&numberFormats[event.code]){event.preventDefault();void applyFormat({number_format:numberFormats[event.code]});return}
    if(event.altKey&&event.shiftKey&&event.code==='Digit5'){event.preventDefault();void applyFormat({strike:activeCell?.style?.strike!==true});return}
    if(primary&&event.shiftKey&&key==='b'){event.preventDefault();void applyFormat({bold:activeCell?.style?.bold!==true});return}
    if(primary&&event.shiftKey&&key==='i'){event.preventDefault();void applyFormat({italic:activeCell?.style?.italic!==true});return}
    if(primary&&event.shiftKey&&key==='u'){event.preventDefault();void applyFormat({underline:activeCell?.style?.underline!==true});return}
    if(primary&&event.shiftKey&&key==='l'){event.preventDefault();void applyFormat({horizontal_align:'left'});return}
    if(primary&&event.shiftKey&&key==='e'){event.preventDefault();void applyFormat({horizontal_align:'center'});return}
    if(primary&&event.shiftKey&&key==='r'){event.preventDefault();void applyFormat({horizontal_align:'right'});return}
    if(primary&&key==='b'){event.preventDefault();void applyFormat({bold:activeCell?.style?.bold!==true});return}
    if(primary&&key==='i'){event.preventDefault();void applyFormat({italic:activeCell?.style?.italic!==true});return}
    if(primary&&key==='u'){event.preventDefault();void applyFormat({underline:activeCell?.style?.underline!==true});return}
    if(primary&&key==='a'){event.preventDefault();gridShortcut({command:'select-all'});return}
    if(primary&&event.code==='Space'){event.preventDefault();gridShortcut({command:'select-column'});return}
    if(event.shiftKey&&event.code==='Space'){event.preventDefault();gridShortcut({command:'select-row'});return}
    if(primary&&['ArrowUp','ArrowDown','ArrowLeft','ArrowRight'].includes(event.key)){event.preventDefault();gridShortcut({command:'move-data-edge',direction:event.key.replace('Arrow','').toLowerCase() as 'up'|'down'|'left'|'right',extend:event.shiftKey});return}
    if(primary&&!event.shiftKey&&key==='d'){event.preventDefault();gridShortcut({command:'fill-down'});return}
    if(primary&&!event.shiftKey&&key==='r'){event.preventDefault();gridShortcut({command:'fill-right'});return}
    if(primary&&event.key==='Home'){event.preventDefault();gridShortcut({command:'move-first'});return}
    if(primary&&event.key==='End'){event.preventDefault();gridShortcut({command:'move-last'})}}
    window.addEventListener('keydown',shortcut);return()=>window.removeEventListener('keydown',shortcut)},[activeCell?.style?.bold,activeCell?.style?.italic,activeCell?.style?.strike,activeCell?.style?.underline,applyFormat,createSheet,revertOperation,saveWorkbook])
  if(workbook.isLoading)return <div className="editor-loading">kanpic 편집기를 준비하는 중…</div>
  // A workbook that exists but is not shared with this user offers the Sheets
  // style request-access flow instead of a dead end.
  if(workbook.error instanceof ApiError&&workbook.error.status===403)return <div className="page-shell"><AppHeader build={build} session={session}/><main className="access-denied">
    <section>
      <Lock/>
      <h1>액세스 권한이 필요합니다</h1>
      <p>이 워크북은 소유자가 지정한 사용자, 부서 또는 역할만 열 수 있습니다. 필요한 권한을 요청하면 소유자에게 전달됩니다.</p>
      {accessRequested
        ?<div className="access-requested" role="status">액세스 요청을 보냈습니다. 소유자가 승인하면 이 링크로 다시 열 수 있습니다.</div>
        :<div className="access-actions">
          <button className="primary" disabled={requestingAccess} onClick={()=>{
            setRequestingAccess(true)
            void api<unknown>(`/api/v1/workbooks/${workbookId}/access-requests`,{method:'POST',body:JSON.stringify({requested_role:'editor'})})
              .then(()=>setAccessRequested(true))
              .catch(reason=>alert(reason instanceof Error?reason.message:'액세스 요청을 보내지 못했습니다.'))
              .finally(()=>setRequestingAccess(false))
          }}>편집 권한 요청</button>
          <button className="secondary" disabled={requestingAccess} onClick={()=>{
            setRequestingAccess(true)
            void api<unknown>(`/api/v1/workbooks/${workbookId}/access-requests`,{method:'POST',body:JSON.stringify({requested_role:'viewer'})})
              .then(()=>setAccessRequested(true))
              .catch(reason=>alert(reason instanceof Error?reason.message:'액세스 요청을 보내지 못했습니다.'))
              .finally(()=>setRequestingAccess(false))
          }}>보기 권한 요청</button>
        </div>}
      <a href="/">워크북 목록으로 돌아가기</a>
    </section>
  </main></div>
  if(!workbook.data||!activeSheet)return <div className="editor-loading error">워크북을 찾을 수 없습니다.</div>
  const accessRole:ShareRole=workbook.data.access_role??'owner'
  const canWrite=accessRole==='editor'||accessRole==='owner'
  const canComment=canWrite||accessRole==='commenter'
  const readOnly=!canWrite
  const conflictCount=Math.max(conflicts.data?.items.length??0,editor.conflicts)
  const displaySaveState=conflictCount>0&&editor.saveState==='saved'?'conflict':editor.saveState
  const saveLabel={saved:'모든 변경사항 저장됨',saving:'저장 중…',offline:'오프라인 · 로컬 저장',conflict:`충돌 ${conflictCount}건`,error:'저장 오류'}[displaySaveState]
  const mergedSelection=Boolean(cellMerge(editor.cells.get(cellKey(editorSelection.startRow,editorSelection.startColumn))))
  const selectedRows=editorSelection.endRow-editorSelection.startRow+1,selectedColumns=editorSelection.endColumn-editorSelection.startColumn+1
  const numberFormatItems:MenuItem[]=[
    {kind:'item',label:'자동',onSelect:()=>void applyFormat({number_format:null})},
    {kind:'item',label:'숫자 1,234.56',shortcut:'Ctrl+Shift+1',onSelect:()=>void applyFormat({number_format:'#,##0.00'})},
    {kind:'item',label:'통화 ₩1,234',shortcut:'Ctrl+Shift+4',onSelect:()=>void applyFormat({number_format:'₩#,##0'})},
    {kind:'item',label:'백분율 12.34%',shortcut:'Ctrl+Shift+5',onSelect:()=>void applyFormat({number_format:'0.00%'})},
    {kind:'item',label:'날짜 2026-08-03',shortcut:'Ctrl+Shift+3',onSelect:()=>void applyFormat({number_format:'yyyy-mm-dd'})},
    {kind:'item',label:'시간 13:45:30',shortcut:'Ctrl+Shift+2',onSelect:()=>void applyFormat({number_format:'hh:mm:ss'})},
    {kind:'item',label:'지수 1.23E+04',shortcut:'Ctrl+Shift+6',onSelect:()=>void applyFormat({number_format:'0.00E+00'})},
  ]
  const menus:WorkbookMenu[]=[
    {label:'파일',items:[
      {kind:'item',label:'저장',shortcut:'Ctrl+S',onSelect:()=>void saveWorkbook()},
      {kind:'item',label:'새 시트 추가',shortcut:'Shift+F11',onSelect:()=>void createSheet()},
      {kind:'item',label:'시트 복제',disabled:!canWrite,onSelect:()=>void duplicateSheet(activeSheet)},
      {kind:'separator'},
      {kind:'item',label:'XLSX로 내보내기',onSelect:()=>void exportWorkbook('xlsx')},
      {kind:'item',label:'현재 시트 CSV로 내보내기',onSelect:()=>void exportWorkbook('csv')},
      {kind:'separator'},
      {kind:'item',label:'공유 설정…',onSelect:()=>setShareOpen(true)},
      {kind:'item',label:'버전 이력',onSelect:()=>setRightPanel('history')},
      {kind:'item',label:'워크북 목록으로',onSelect:()=>{window.location.href='/'}},
    ]},
    {label:'수정',items:[
      {kind:'item',label:'실행 취소',shortcut:'Ctrl+Z',disabled:editor.undoStack.length===0,onSelect:()=>void revertOperation('undo')},
      {kind:'item',label:'다시 실행',shortcut:'Ctrl+Y',disabled:editor.redoStack.length===0,onSelect:()=>void revertOperation('redo')},
      {kind:'separator'},
      {kind:'item',label:'잘라내기',shortcut:'Ctrl+X',onSelect:()=>gridShortcut({command:'cut'})},
      {kind:'item',label:'복사',shortcut:'Ctrl+C',onSelect:()=>gridShortcut({command:'copy'})},
      {kind:'item',label:'붙여넣기',shortcut:'Ctrl+V',onSelect:()=>gridShortcut({command:'paste'})},
      {kind:'item',label:'값만 붙여넣기',shortcut:'Ctrl+Shift+V',onSelect:()=>gridShortcut({command:'paste-values'})},
      {kind:'separator'},
      {kind:'item',label:'내용 지우기',shortcut:'Delete',onSelect:()=>gridShortcut({command:'clear-contents'})},
      {kind:'item',label:'서식 지우기',shortcut:'Ctrl+\\',onSelect:()=>void clearFormat()},
      {kind:'separator'},
      {kind:'item',label:'찾기',shortcut:'Ctrl+F',onSelect:()=>openSearch(false)},
      {kind:'item',label:'찾기 및 바꾸기',shortcut:'Ctrl+H',onSelect:()=>openSearch(true)},
      {kind:'item',label:'전체 선택',shortcut:'Ctrl+A',onSelect:()=>gridShortcut({command:'select-all'})},
      {kind:'item',label:'데이터 영역 선택',shortcut:'Ctrl+Shift+A',onSelect:()=>gridShortcut({command:'select-data-region'})},
    ]},
    {label:'보기',items:[
      {kind:'item',label:'수식 표시',shortcut:'Ctrl+`',checked:showFormulas,onSelect:()=>setShowFormulas(current=>!current)},
      {kind:'separator'},
      {kind:'item',label:'확대',onSelect:()=>editor.setZoom(editor.zoom+.1)},
      {kind:'item',label:'축소',onSelect:()=>editor.setZoom(editor.zoom-.1)},
      {kind:'item',label:'100%로 보기',onSelect:()=>editor.setZoom(1)},
      {kind:'separator'},
      {kind:'item',label:'활성 셀 앞까지 고정',onSelect:()=>void freezeToSelection()},
      {kind:'item',label:'고정 해제',disabled:(activeSheet.layout?.frozen_rows??0)===0&&(activeSheet.layout?.frozen_columns??0)===0,onSelect:()=>void applyLayout({action:'freeze',frozen_rows:0,frozen_columns:0})},
      {kind:'item',label:'모든 행 표시',onSelect:()=>void applyLayout({action:'show_all',axis:'row'})},
      {kind:'item',label:'모든 열 표시',onSelect:()=>void applyLayout({action:'show_all',axis:'column'})},
      {kind:'separator'},
      {kind:'item',label:'시트 레이아웃…',onSelect:()=>setLayoutOpen(true)},
      {kind:'item',label:'단축키 목록',shortcut:'Ctrl+/',onSelect:()=>setShortcutsOpen(true)},
    ]},
    {label:'삽입',items:[
      {kind:'item',label:`위에 행 ${selectedRows}개 삽입`,onSelect:()=>void applyStructure({axis:'row',action:'insert',index:editorSelection.startRow,count:selectedRows})},
      {kind:'item',label:`아래에 행 ${selectedRows}개 삽입`,onSelect:()=>void applyStructure({axis:'row',action:'insert',index:editorSelection.endRow+1,count:selectedRows})},
      {kind:'item',label:`왼쪽에 열 ${selectedColumns}개 삽입`,onSelect:()=>void applyStructure({axis:'column',action:'insert',index:editorSelection.startColumn,count:selectedColumns})},
      {kind:'item',label:`오른쪽에 열 ${selectedColumns}개 삽입`,onSelect:()=>void applyStructure({axis:'column',action:'insert',index:editorSelection.endColumn+1,count:selectedColumns})},
      {kind:'item',label:'행과 열 관리…',onSelect:()=>setStructureOpen(true)},
      {kind:'separator'},
      {kind:'item',label:'차트…',disabled:!canWrite,onSelect:()=>setChartDialog(null)},
      {kind:'item',label:'피벗 테이블…',disabled:!canWrite,onSelect:()=>setPivotDialog(null)},
      {kind:'item',label:'댓글',shortcut:'Ctrl+Alt+M',disabled:!canComment,onSelect:()=>setRightPanel('comments')},
      {kind:'item',label:'이름 범위…',onSelect:()=>setNamedRangeOpen(true)},
      {kind:'item',label:'자동 합계',shortcut:'Alt+=',onSelect:()=>gridShortcut({command:'auto-sum'})},
      {kind:'separator'},
      {kind:'item',label:'오늘 날짜',shortcut:'Ctrl+;',onSelect:()=>gridShortcut({command:'insert-today'})},
      {kind:'item',label:'현재 시간',shortcut:'Ctrl+Shift+;',onSelect:()=>gridShortcut({command:'insert-now'})},
    ]},
    {label:'서식',items:[
      {kind:'item',label:'굵게',shortcut:'Ctrl+B',checked:activeCell?.style?.bold===true,onSelect:()=>void applyFormat({bold:activeCell?.style?.bold!==true})},
      {kind:'item',label:'기울임',shortcut:'Ctrl+I',checked:activeCell?.style?.italic===true,onSelect:()=>void applyFormat({italic:activeCell?.style?.italic!==true})},
      {kind:'item',label:'밑줄',shortcut:'Ctrl+U',checked:activeCell?.style?.underline===true,onSelect:()=>void applyFormat({underline:activeCell?.style?.underline!==true})},
      {kind:'item',label:'취소선',shortcut:'Alt+Shift+5',checked:activeCell?.style?.strike===true,onSelect:()=>void applyFormat({strike:activeCell?.style?.strike!==true})},
      {kind:'submenu',label:'표시 형식',items:numberFormatItems},
      {kind:'submenu',label:'맞춤',items:[
        {kind:'item',label:'왼쪽 정렬',shortcut:'Ctrl+Shift+L',onSelect:()=>void applyFormat({horizontal_align:'left'})},
        {kind:'item',label:'가운데 정렬',shortcut:'Ctrl+Shift+E',onSelect:()=>void applyFormat({horizontal_align:'center'})},
        {kind:'item',label:'오른쪽 정렬',shortcut:'Ctrl+Shift+R',onSelect:()=>void applyFormat({horizontal_align:'right'})},
        {kind:'item',label:'위쪽 맞춤',onSelect:()=>void applyFormat({vertical_align:'top'})},
        {kind:'item',label:'가운데 맞춤',onSelect:()=>void applyFormat({vertical_align:'middle'})},
        {kind:'item',label:'아래쪽 맞춤',onSelect:()=>void applyFormat({vertical_align:'bottom'})},
        {kind:'item',label:'자동 줄바꿈',onSelect:()=>void applyFormat({text_mode:'wrap'})},
        {kind:'item',label:'넘치게 표시',onSelect:()=>void applyFormat({text_mode:'overflow'})},
        {kind:'item',label:'잘라서 표시',onSelect:()=>void applyFormat({text_mode:'clip'})},
      ]},
      {kind:'separator'},
      {kind:'item',label:mergedSelection?'셀 병합 해제':'셀 병합',onSelect:()=>void changeMerge(!mergedSelection)},
      {kind:'item',label:'조건부 서식…',onSelect:()=>setConditionalFormatOpen(true)},
      {kind:'item',label:'서식 세부 설정…',onSelect:()=>setFormatOpen(true)},
      {kind:'item',label:'서식 지우기',shortcut:'Ctrl+\\',onSelect:()=>void clearFormat()},
    ]},
    {label:'데이터',items:[
      {kind:'item',label:'범위 정렬…',onSelect:()=>setSortOpen(true)},
      {kind:'item',label:'필터 보기…',onSelect:()=>setFilterOpen(true)},
      {kind:'item',label:'데이터 검증…',onSelect:()=>setValidationOpen(true)},
      {kind:'item',label:'피벗 테이블…',onSelect:()=>setPivotDialog(null)},
      {kind:'item',label:'이름 범위…',onSelect:()=>setNamedRangeOpen(true)},
      {kind:'separator'},
      {kind:'item',label:`선택 행 숨기기 (${selectedRows}개)`,shortcut:'Ctrl+Alt+9',onSelect:()=>void hideSelection('row')},
      {kind:'item',label:`선택 열 숨기기 (${selectedColumns}개)`,shortcut:'Ctrl+Alt+0',onSelect:()=>void hideSelection('column')},
      {kind:'separator'},
      {kind:'item',label:'워크북 검색',shortcut:'Ctrl+F',onSelect:()=>openSearch(false)},
    ]},
    {label:'도구',items:[
      {kind:'item',label:'AI 도우미',checked:rightPanel==='ai',onSelect:()=>setRightPanel(current=>current==='ai'?null:'ai')},
      {kind:'item',label:'자동화',checked:rightPanel==='automation',disabled:!canWrite,onSelect:()=>setRightPanel(current=>current==='automation'?null:'automation')},
      {kind:'item',label:'차트 패널',checked:rightPanel==='charts',onSelect:()=>setRightPanel(current=>current==='charts'?null:'charts')},
      {kind:'item',label:'피벗 패널',checked:rightPanel==='pivots',onSelect:()=>setRightPanel(current=>current==='pivots'?null:'pivots')},
      {kind:'item',label:'댓글',checked:rightPanel==='comments',onSelect:()=>setRightPanel(current=>current==='comments'?null:'comments')},
      {kind:'item',label:`편집 충돌${conflictCount>0?` (${conflictCount})`:''}`,checked:rightPanel==='conflicts',onSelect:()=>setRightPanel(current=>current==='conflicts'?null:'conflicts')},
      {kind:'separator'},
      {kind:'item',label:'개인 환경설정',onSelect:()=>{window.location.href='/preferences'}},
    ]},
    {label:'도움말',items:[
      {kind:'item',label:'단축키 목록',shortcut:'Ctrl+/',onSelect:()=>setShortcutsOpen(true)},
      {kind:'label',label:`kanpic ${build?.version??''}`.trim()},
    ]},
  ]
  return <div className="editor-shell"><AppHeader build={build} session={session}><div className="editor-title"><a href="/" aria-label="뒤로"><ChevronLeft/></a><div><strong>{workbook.data.title}</strong><small className={conflictCount>0?'interactive':''} role={conflictCount>0?'button':undefined} tabIndex={conflictCount>0?0:undefined} onClick={conflictCount>0?()=>setRightPanel('conflicts'):undefined} onKeyDown={conflictCount>0?event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();setRightPanel('conflicts')}}:undefined}><span className={`save-dot ${displaySaveState}`}/>{saveLabel} · v{serverVersion}</small></div>{accessRole!=='owner'&&<span className={`access-badge ${accessRole}`} title={accessSummary({role:accessRole,source:workbook.data.access_source,source_label:workbook.data.access_source})}>{accessRole==='viewer'?<><Eye/> 보기 전용</>:accessRole==='commenter'?<><MessageCircle/> 댓글 가능</>:<>편집자</>}</span>}</div></AppHeader>
    <div className="editor-actions"><WorkbookMenuBar menus={menus}/><div className="share-actions"><span className={`collaboration-count ${collaborationStatus}`} title={Object.values(collaborators).map(user=>user.actor_id).join(', ')}><i/>{collaborationStatus==='connected'?`${Object.keys(collaborators).length}명 접속`:collaborationStatus==='offline'?'오프라인':'재연결 중'}</span><button className="ghost" onClick={()=>exportWorkbook('xlsx')} title="XLSX로 내보내기"><Download/> XLSX 내보내기</button><button className="ghost" onClick={()=>exportWorkbook('csv')} title="현재 시트를 CSV로 내보내기">CSV</button><button className="primary" onClick={()=>setShareOpen(true)}><Share2/> 공유{(workbook.data.shared_count??0)>0?` ${workbook.data.shared_count}`:''}</button></div></div>
    <div className="toolbar"><button aria-label="실행 취소" title="실행 취소" disabled={readOnly||editor.undoStack.length===0} onClick={()=>revertOperation('undo')}><Undo2/></button><button aria-label="다시 실행" title="다시 실행" disabled={readOnly||editor.redoStack.length===0} onClick={()=>revertOperation('redo')}><Redo2/></button><button aria-label="워크북 검색" title="워크북 검색 (Ctrl/⌘+F)" onClick={()=>setSearchOpen(true)}><Search/></button><span className="divider"/><select aria-label="글꼴" className="toolbar-select font-family" disabled={readOnly} value={typeof activeCell?.style?.font_family==='string'?activeCell.style.font_family:'Inter'} onChange={event=>void applyFormat({font_family:event.target.value})}><option>Inter</option><option>Pretendard</option><option>Arial</option><option>Georgia</option><option>monospace</option></select><select aria-label="글꼴 크기" className="toolbar-select font-size" disabled={readOnly} value={typeof activeCell?.style?.font_size==='number'?activeCell.style.font_size:12} onChange={event=>void applyFormat({font_size:Number(event.target.value)})}>{[8,9,10,11,12,14,16,18,20,24,28,32].map(size=><option key={size}>{size}</option>)}</select><button aria-label="굵게" title="굵게" disabled={readOnly} className={activeCell?.style?.bold===true?'active':''} onClick={()=>void applyFormat({bold:activeCell?.style?.bold!==true})}><Bold/></button><button aria-label="기울임" title="기울임" disabled={readOnly} className={activeCell?.style?.italic===true?'active':''} onClick={()=>void applyFormat({italic:activeCell?.style?.italic!==true})}><Italic/></button><button aria-label="밑줄" title="밑줄" disabled={readOnly} className={activeCell?.style?.underline===true?'active':''} onClick={()=>void applyFormat({underline:activeCell?.style?.underline!==true})}><Underline/></button><label className="toolbar-color" title="글자색"><span>A</span><input aria-label="글자색" type="color" disabled={readOnly} value={typeof activeCell?.style?.color==='string'?activeCell.style.color:'#1c2733'} onChange={event=>void applyFormat({color:event.target.value})}/></label><label className="toolbar-color background" title="셀 배경색"><span>▰</span><input aria-label="셀 배경색" type="color" disabled={readOnly} value={typeof activeCell?.style?.background==='string'?activeCell.style.background:'#ffffff'} onChange={event=>void applyFormat({background:event.target.value})}/></label><span className="divider"/><button aria-label="왼쪽 정렬" title="왼쪽 정렬" disabled={readOnly} className={activeCell?.style?.horizontal_align==='left'?'active':''} onClick={()=>void applyFormat({horizontal_align:'left'})}><AlignLeft/></button><button aria-label="가운데 정렬" title="가운데 정렬" disabled={readOnly} className={activeCell?.style?.horizontal_align==='center'?'active':''} onClick={()=>void applyFormat({horizontal_align:'center'})}><AlignCenter/></button><button aria-label="오른쪽 정렬" title="오른쪽 정렬" disabled={readOnly} className={activeCell?.style?.horizontal_align==='right'?'active':''} onClick={()=>void applyFormat({horizontal_align:'right'})}><AlignRight/></button><button aria-label={mergedSelection?'병합 해제':'셀 병합'} title={mergedSelection?'병합 해제':'셀 병합'} disabled={readOnly} className={mergedSelection?'active':''} onClick={()=>void changeMerge(!mergedSelection)}>{mergedSelection?<TableCellsSplit/>:<TableCellsMerge/>}</button><button aria-label="범위 정렬" title="범위 정렬" disabled={readOnly} onClick={()=>setSortOpen(true)}><ArrowUpDown/></button><button aria-label="필터 보기" title="필터 보기" className={activeFilter?'active':''} onClick={()=>setFilterOpen(true)}><Filter/></button><button aria-label="데이터 검증" title="데이터 검증" disabled={readOnly} className={(validations.data?.items.length??0)>0?'active':''} onClick={()=>setValidationOpen(true)}><BadgeCheck/></button><button aria-label="조건부 서식" title="조건부 서식" disabled={readOnly} className={(conditionalFormats.data?.items.length??0)>0?'active':''} onClick={()=>setConditionalFormatOpen(true)}><Palette/></button><button aria-label="선택 범위 링크 복사" title="선택 범위 링크 복사" onClick={()=>void copySelectionLink()}><Link2/></button><span className="toolbar-spacer"/><button onClick={()=>editor.setZoom(editor.zoom-.1)}><ZoomOut/></button><span className="zoom-value">{Math.round(editor.zoom*100)}%</span><button onClick={()=>editor.setZoom(editor.zoom+.1)}><ZoomIn/></button><button aria-label="AI 도우미" title="AI 도우미" onClick={()=>setRightPanel(current=>current==='ai'?null:'ai')} className={rightPanel==='ai'?'active':''}><Bot/></button><button aria-label="편집 충돌" title={`편집 충돌 ${conflictCount}건`} onClick={()=>setRightPanel(current=>current==='conflicts'?null:'conflicts')} className={rightPanel==='conflicts'||conflictCount>0?'active':''}><AlertTriangle/></button><button aria-label="버전 이력" title="버전 이력" onClick={()=>setRightPanel(current=>current==='history'?null:'history')} className={rightPanel==='history'?'active':''}><History/></button><button aria-label="댓글" title="댓글" onClick={()=>setRightPanel(current=>current==='comments'?null:'comments')} className={rightPanel==='comments'?'active':''}><MessageSquare/></button><button aria-label="추가 도구" title="추가 도구" aria-haspopup="menu" aria-expanded={Boolean(overflowMenu)} onClick={event=>{const rect=event.currentTarget.getBoundingClientRect();setOverflowMenu(current=>current?undefined:{x:Math.max(8,rect.right-252),y:rect.bottom+4})}}><MoreHorizontal/></button></div>
    {overflowMenu&&<ContextMenu x={overflowMenu.x} y={overflowMenu.y} label="추가 도구 메뉴" onClose={()=>setOverflowMenu(undefined)} items={[
      {kind:'item',label:'수식 표시',shortcut:'Ctrl+`',checked:showFormulas,onSelect:()=>setShowFormulas(current=>!current)},
      {kind:'item',label:'서식 지우기',shortcut:'Ctrl+\\',onSelect:()=>void clearFormat()},
      {kind:'separator'},
      {kind:'item',label:'차트 패널',checked:rightPanel==='charts',onSelect:()=>setRightPanel(current=>current==='charts'?null:'charts')},
      {kind:'item',label:'피벗 패널',checked:rightPanel==='pivots',onSelect:()=>setRightPanel(current=>current==='pivots'?null:'pivots')},
      {kind:'item',label:'자동화',checked:rightPanel==='automation',onSelect:()=>setRightPanel(current=>current==='automation'?null:'automation')},
      {kind:'separator'},
      {kind:'item',label:'찾기 및 바꾸기',shortcut:'Ctrl+H',onSelect:()=>openSearch(true)},
      {kind:'item',label:'단축키 목록',shortcut:'Ctrl+/',onSelect:()=>setShortcutsOpen(true)},
    ]}/>}
    <div className="formula-bar"><form onSubmit={event=>{event.preventDefault();submitNameBox()}}><input className="name-box" ref={nameBoxRef} aria-label="이름 상자" list="named-range-options" value={nameBoxValue} onChange={event=>setNameBoxValue(event.target.value)} onBlur={()=>{if(!nameBoxValue.trim())setNameBoxValue(selectionAddress)}}/><datalist id="named-range-options">{(namedRanges.data?.items??[]).map(item=><option key={item.id} value={item.name}>{item.range}</option>)}</datalist></form><button className="named-range-trigger" aria-label="이름 범위 관리" title="이름 범위 관리" onClick={()=>setNamedRangeOpen(true)}><Link2/></button><span>fx</span><input value={formula} readOnly onDoubleClick={()=>{const source=activeCell?.spill_source&&parseCellAddress(activeCell.spill_source);if(source)editor.select(source.row,source.column);editor.setEditing(true)}} aria-label="수식 입력창"/></div>
    <div className="editor-body"><div className="sheet-area"><CanvasGrid sheetId={activeSheet.id} layout={activeSheet.layout} version={serverVersion} onVersion={updateVersion} hiddenRows={filterResult.data?.hidden_rows??[]} validations={validations.data?.items??[]} conditionalFormats={conditionalFormats.data?.items??[]} showFormulas={showFormulas} readOnly={readOnly} onLayout={applyLayout} onStructure={applyStructure} onMenuCommand={handleGridMenu}/><SheetTabs sheets={workbook.data.sheets} activeSheetId={activeSheet.id} saveState={displaySaveState} saveLabel={activeFilter&&filterResult.data?`${saveLabel} · 필터 ${filterResult.data.visible_count.toLocaleString()}행` :saveLabel} onStatusClick={conflictCount>0?()=>setRightPanel('conflicts'):undefined} onSelect={setActiveSheet} onCreate={createSheet} onRename={(sheet,name)=>updateSheet(sheet,{name})} onDuplicate={duplicateSheet} onMove={(sheet,position)=>updateSheet(sheet,{position})} onColor={(sheet,color)=>updateSheet(sheet,{color})} onDelete={deleteSheet}/></div>
      {rightPanel==='ai'&&<AIPanel workbookId={workbookId} sheetId={activeSheet.id} selectionRange={selectionAddress} baseVersion={serverVersion} onClose={()=>setRightPanel(null)} onExecuted={handleAIExecuted}/>}
      {rightPanel==='automation'&&<AutomationPanel workbookId={workbookId} sheets={workbook.data.sheets} activeSheetId={activeSheet.id} selectionRange={selectionAddress} onClose={()=>setRightPanel(null)} onExecuted={handleAutomationExecuted}/>}
      {rightPanel==='history'&&<VersionPanel workbookId={workbookId} currentVersion={serverVersion} onClose={()=>setRightPanel(null)} onRestored={handleRestored}/>}
      {rightPanel==='comments'&&<CommentPanel workbookId={workbookId} sheetId={activeSheet.id} selectionRange={selectionAddress} currentActor={session?.user?.id??'local-user'} focusThreadId={routeNavigation.commentId||undefined} onNavigate={navigateToRange} onClose={()=>setRightPanel(null)}/>}
      {rightPanel==='conflicts'&&<ConflictPanel workbookId={workbookId} sheets={workbook.data.sheets} currentActor={session?.user?.id??'local-user'} onClose={()=>setRightPanel(null)} onNavigate={navigateToRange} onResolved={handleConflictResolved}/>}
    </div>
    <ChartOverlay charts={charts.data?.items??[]} version={serverVersion} onEdit={item=>setChartDialog(item)} onUpdate={updateChart}/>
    {rightPanel==='charts'&&<ChartPanel charts={charts.data?.items??[]} sheets={workbook.data.sheets} onClose={()=>setRightPanel(null)} onCreate={()=>setChartDialog(null)} onEdit={item=>setChartDialog(item)} onNavigate={item=>{if(item.source_sheet_id&&item.source_range!=='#REF!')navigateToRange(item.source_sheet_id,item.source_range)}}/>}
    {rightPanel==='pivots'&&<PivotPanel pivots={pivots.data?.items??[]} sheets={workbook.data.sheets} onClose={()=>setRightPanel(null)} onCreate={()=>setPivotDialog(null)} onEdit={item=>setPivotDialog(item)} onOpen={setPivotResult} onRefresh={refreshPivot} onNavigate={item=>{if(item.source_sheet_id&&item.source_range!=='#REF!')navigateToRange(item.source_sheet_id,item.source_range)}}/>}
    <button className={`chart-launcher ${rightPanel==='charts'?'active':''}`} aria-label="차트 패널" onClick={()=>setRightPanel(current=>current==='charts'?null:'charts')}><BarChart3/> 차트</button>
    <button className={`pivot-launcher ${rightPanel==='pivots'?'active':''}`} aria-label="피벗 패널" onClick={()=>setRightPanel(current=>current==='pivots'?null:'pivots')}><Table2/> 피벗</button>
    {canWrite&&<button className={`automation-launcher ${rightPanel==='automation'?'active':''}`} aria-label="자동화 패널" onClick={()=>setRightPanel(current=>current==='automation'?null:'automation')}><Workflow/> 자동화</button>}
    {chartDialog!==undefined&&<ChartDialog chart={chartDialog??undefined} activeSheetId={activeSheet.id} selectionRange={selectionAddress} sheets={workbook.data.sheets} onClose={()=>setChartDialog(undefined)} onCreate={createChart} onUpdate={updateChart} onDelete={deleteChart}/>}
    {pivotDialog!==undefined&&<PivotDialog pivot={pivotDialog??undefined} activeSheetId={activeSheet.id} selectionRange={selectionAddress} sheets={workbook.data.sheets} onClose={()=>setPivotDialog(undefined)} onCreate={createPivot} onUpdate={updatePivot} onDelete={deletePivot}/>}
    {pivotResult&&<PivotResultDialog pivot={pivotResult} version={serverVersion} onClose={()=>setPivotResult(undefined)} onRefresh={refreshPivot}/>}
    {sortOpen&&<SortDialog range={editorSelection} onClose={()=>setSortOpen(false)} onSort={sortSelection}/>}
    {structureOpen&&<StructureDialog range={editorSelection} onClose={()=>setStructureOpen(false)} onApply={applyStructure}/>}
    {layoutOpen&&<LayoutDialog range={editorSelection} layout={activeSheet.layout} onClose={()=>setLayoutOpen(false)} onApply={applyLayout}/>}
    {formatOpen&&<FormatDialog style={activeCell?.style} onClose={()=>setFormatOpen(false)} onApply={applyFormat}/>}
    {filterOpen&&<FilterDialog range={editorSelection} views={filterViews.data?.items??[]} result={filterResult.data} onClose={()=>setFilterOpen(false)} onCreate={createFilter} onUpdate={updateFilter} onDelete={deleteFilter}/>} 
    {validationOpen&&<DataValidationDialog range={editorSelection} rules={validations.data?.items??[]} onClose={()=>setValidationOpen(false)} onCreate={createValidation} onUpdate={updateValidation} onDelete={deleteValidation} onEvaluate={evaluateValidation}/>} 
    {conditionalFormatOpen&&<ConditionalFormatDialog range={editorSelection} rules={conditionalFormats.data?.items??[]} onClose={()=>setConditionalFormatOpen(false)} onCreate={createConditionalFormat} onUpdate={updateConditionalFormat} onDelete={deleteConditionalFormat}/>}
    {namedRangeOpen&&<NamedRangeDialog selection={editorSelection} activeSheetId={activeSheet.id} sheets={workbook.data.sheets} ranges={namedRanges.data?.items??[]} onClose={()=>setNamedRangeOpen(false)} onCreate={createNamedRange} onUpdate={updateNamedRange} onDelete={deleteNamedRange} onNavigate={item=>{navigateToRange(item.sheet_id,item.range);setNamedRangeOpen(false)}}/>}
    {shareOpen&&<ShareDialog workbook={workbook.data} onClose={()=>setShareOpen(false)} onChanged={()=>{void client.invalidateQueries({queryKey:['workbook',workbookId]})}}/>}
    {shortcutsOpen&&<WorkbookShortcutsDialog onClose={()=>setShortcutsOpen(false)}/>}
    <WorkbookSearchDialog open={searchOpen} workbookId={workbookId} version={serverVersion} sheetId={activeSheet.id} sheetName={activeSheet.name} replaceMode={replaceMode} onClose={()=>{setSearchOpen(false);setReplaceMode(false)}} onNavigate={(item:WorkbookSearchMatch)=>navigateToRange(item.sheet_id,item.address)} onReplaced={result=>void handleReplaced(result)}/>
  </div>
}
