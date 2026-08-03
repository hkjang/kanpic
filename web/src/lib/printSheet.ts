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
  const rows:string[]=[]
  if(region){
    if(options.headers){
      const headerCells=[`<th class="corner"></th>`]
      for(let column=region.startColumn;column<=region.endColumn;column+=1)headerCells.push(`<th>${escapeHTML(address(1,column).replace(/\d+$/,''))}</th>`)
      rows.push(`<tr>${headerCells.join('')}</tr>`)
    }
    for(let row=region.startRow;row<=region.endRow;row+=1){
      const columns:string[]=options.headers?[`<th class="row-head">${row}</th>`]:[]
      for(let column=region.startColumn;column<=region.endColumn;column+=1){
        const cell=cells.get(cellKey(row,column))
        const text=cell?formatCellValue(cell.value,cell.style):''
        columns.push(`<td style="${cellCSS(cell?.style)}">${escapeHTML(text)}</td>`)
      }
      rows.push(`<tr>${columns.join('')}</tr>`)
    }
  }
  const empty=rows.length===0?'<p class="empty">인쇄할 데이터가 없습니다.</p>':''
  return `<!doctype html><html lang="ko"><head><meta charset="utf-8"><title>${escapeHTML(options.title)}</title><style>
  @page{margin:14mm}
  body{font:12px Inter,Pretendard,'Malgun Gothic',sans-serif;color:#1c2b33;margin:0}
  header{display:flex;justify-content:space-between;align-items:baseline;margin-bottom:10px}
  h1{font-size:15px;margin:0}
  header span{font-size:11px;color:#61727c}
  table{border-collapse:collapse;width:100%}
  td,th{border:${options.gridlines?'1px solid #d6dee2':'none'};padding:3px 6px;font-weight:inherit;text-align:left;vertical-align:bottom}
  th{background:#f1f5f7;font-weight:600;text-align:center;color:#4a5b65}
  th.row-head{width:34px}
  .empty{color:#7d8b94}
  tr{page-break-inside:avoid}
  </style></head><body><header><h1>${escapeHTML(options.title)}</h1><span>${escapeHTML(options.sheetName)}${region?` · ${address(region.startRow,region.startColumn)}:${address(region.endRow,region.endColumn)}`:''}</span></header>${empty||`<table>${rows.join('')}</table>`}</body></html>`
}
