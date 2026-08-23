import { address } from './api'
import { formatCellValue } from './cellFormat'
import { cellKey } from '../state/editor'
import type { Cell } from '../types'
import type { GridRegion } from './dataRegion'

export type PrintOptions={
  title:string
  sheetName:string
  region?:GridRegion
  gridlines:boolean
  headers:boolean
  /**
   * Rows the reader cannot see — filtered out, hidden, or folded into a
   * collapsed group. Printing them would hand somebody a page that disagrees
   * with the screen they printed it from.
   */
  hiddenRows?:Set<number>
  /**
   * 열의 실제 너비(px). 주지 않으면 기본 너비로 셈한다. 이것이 있어야 종이
   * 한 장에 몇 열이 들어가는지 알 수 있다.
   */
  columnWidth?:(column:number)=>number
}

/**
 * 한 장에 들어가는 가로 폭(px). A4 세로에 좌우 14mm 여백이면 210 − 28 =
 * 182mm 이고, 96dpi 에서 1mm 는 약 3.78px 이므로 688px 이다. 브라우저가
 * 실제로 어떤 용지를 쓸지는 알 수 없으므로 가장 흔한 A4 세로를 기준으로
 * 삼는다. 넘치면 다음 장으로 넘길 뿐 잘리지는 않는다.
 */
const PRINTABLE_WIDTH=688
const ROW_HEAD_WIDTH=34
const DEFAULT_PRINT_COLUMN_WIDTH=108

/**
 * 종이 한 장에 들어갈 만큼씩 열을 끊는다. 예전에는 표를 종이 폭에 맞춰
 * 통째로 눌러 담아, 열이 서른 개면 한 열이 몇 밀리미터로 찌그러졌다.
 */
export function columnPages(startColumn:number,endColumn:number,widthOf:(column:number)=>number){
  const pages:Array<{start:number;end:number;widths:number[]}>=[]
  let current:{start:number;end:number;widths:number[]}|undefined
  let used=0
  for(let column=startColumn;column<=endColumn;column+=1){
    const width=Math.max(1,Math.round(widthOf(column)))
    // 한 열이 종이보다 넓어도 혼자서는 한 장을 차지해야 한다. 그렇지 않으면
    // 어느 장에도 들어가지 못한다.
    if(current&&used+width>PRINTABLE_WIDTH-ROW_HEAD_WIDTH){current=undefined;used=0}
    if(!current){current={start:column,end:column,widths:[]};pages.push(current)}
    current.end=column
    current.widths.push(width)
    used+=width
  }
  return pages
}

const escapeHTML=(value:string)=>value.replace(/[&<>"]/g,character=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[character] as string))

/** The smallest rectangle that holds every non-empty cell of the sheet. */
export function usedRegion(cells:Map<string,Cell>):GridRegion|undefined{
  let startRow=Infinity,startColumn=Infinity,endRow=0,endColumn=0
  cells.forEach(cell=>{
    const empty=(cell.value==null||cell.value==='')&&!cell.formula
    if(empty)return
    startRow=Math.min(startRow,cell.row);startColumn=Math.min(startColumn,cell.column)
    endRow=Math.max(endRow,cell.row);endColumn=Math.max(endColumn,cell.column)
  })
  if(endRow===0||endColumn===0)return undefined
  return {startRow,startColumn,endRow,endColumn}
}

function cellCSS(style?:Record<string,unknown>){
  if(!style)return ''
  const rules:string[]=[]
  if(style.bold===true)rules.push('font-weight:700')
  if(style.italic===true)rules.push('font-style:italic')
  const decorations=[style.underline===true?'underline':'',style.strike===true?'line-through':''].filter(Boolean)
  if(decorations.length)rules.push(`text-decoration:${decorations.join(' ')}`)
  if(typeof style.color==='string')rules.push(`color:${style.color}`)
  if(typeof style.background==='string')rules.push(`background:${style.background}`)
  if(typeof style.font_size==='number')rules.push(`font-size:${style.font_size}px`)
  if(typeof style.font_family==='string')rules.push(`font-family:${style.font_family}`)
  if(typeof style.horizontal_align==='string')rules.push(`text-align:${style.horizontal_align}`)
  if(typeof style.vertical_align==='string')rules.push(`vertical-align:${style.vertical_align==='middle'?'middle':style.vertical_align}`)
  if(style.text_mode==='wrap'||style.wrap===true)rules.push('white-space:pre-wrap')
  return rules.join(';')
}

/**
 * Renders the sheet as a plain HTML table. The canvas grid cannot be printed
 * directly, so printing goes through a document the browser can paginate.
 */
export function printableDocument(cells:Map<string,Cell>,options:PrintOptions){
  const region=options.region??usedRegion(cells)
  const widthOf=options.columnWidth??(()=>DEFAULT_PRINT_COLUMN_WIDTH)
  const tables:string[]=[]
  let printedRows=0
  if(region){
    // 종이 폭을 넘는 열은 다음 장으로 넘긴다. 장마다 열 머리글과 행 번호를
    // 다시 찍어야 어느 칸이 무슨 열의 몇 행인지 알 수 있다.
    for(const page of columnPages(region.startColumn,region.endColumn,widthOf)){
      const columnGroup=[options.headers?`<col style="width:${ROW_HEAD_WIDTH}px">`:'']
        .concat(page.widths.map(width=>`<col style="width:${width}px">`)).join('')
      let head=''
      if(options.headers){
        const headerCells=[`<th class="corner"></th>`]
        for(let column=page.start;column<=page.end;column+=1)headerCells.push(`<th>${escapeHTML(address(1,column).replace(/\d+$/,''))}</th>`)
        head=`<thead><tr>${headerCells.join('')}</tr></thead>`
      }
      const rows:string[]=[]
      for(let row=region.startRow;row<=region.endRow;row+=1){
        if(options.hiddenRows?.has(row))continue
        const columns:string[]=options.headers?[`<th class="row-head">${row}</th>`]:[]
        for(let column=page.start;column<=page.end;column+=1){
          const cell=cells.get(cellKey(row,column))
          const text=cell?formatCellValue(cell.value,cell.style):''
          columns.push(`<td style="${cellCSS(cell?.style)}">${escapeHTML(text)}</td>`)
        }
        rows.push(`<tr>${columns.join('')}</tr>`)
      }
      printedRows=rows.length
      if(rows.length===0)continue
      tables.push(`<table><colgroup>${columnGroup}</colgroup>${head}<tbody>${rows.join('')}</tbody></table>`)
    }
  }
  const empty=printedRows===0?'<p class="empty">인쇄할 데이터가 없습니다.</p>':''
  return `<!doctype html><html lang="ko"><head><meta charset="utf-8"><title>${escapeHTML(options.title)}</title><style>
  @page{margin:14mm}
  body{font:12px Inter,Pretendard,'Malgun Gothic',sans-serif;color:#1c2b33;margin:0}
  header{display:flex;justify-content:space-between;align-items:baseline;margin-bottom:10px}
  h1{font-size:15px;margin:0}
  header span{font-size:11px;color:#61727c}
  /* 폭에 맞춰 눌러 담지 않는다. 열은 화면에서와 같은 너비로 찍히고,
     한 장에 안 들어가면 다음 장으로 넘어간다. */
  table{border-collapse:collapse;table-layout:fixed}
  table+table{page-break-before:always;margin-top:10px}
  td,th{border:${options.gridlines?'1px solid #d6dee2':'none'};padding:3px 6px;font-weight:inherit;text-align:left;vertical-align:bottom}
  th{background:#f1f5f7;font-weight:600;text-align:center;color:#4a5b65}
  th.row-head{width:${ROW_HEAD_WIDTH}px}
  td,th{overflow:hidden;text-overflow:ellipsis}
  .empty{color:#7d8b94}
  tr{page-break-inside:avoid}
  thead{display:table-header-group}
  </style></head><body><header><h1>${escapeHTML(options.title)}</h1><span>${escapeHTML(options.sheetName)}${region?` · ${address(region.startRow,region.startColumn)}:${address(region.endRow,region.endColumn)}`:''}</span></header>${empty||tables.join('')}</body></html>`
}
