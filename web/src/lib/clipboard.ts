export const KANPIC_CLIPBOARD_TYPE = 'application/x-kanpic-cells+json'
export const MAX_PASTE_CELLS = 10_000
export const MAX_GRID_ROWS = 10_000
export const MAX_GRID_COLUMNS = 500

export type ClipboardCell = {
  rowOffset:number
  columnOffset:number
  value?:unknown
  formula?:string
  style?:Record<string,unknown>
}

export type KanpicClipboard = {
  version:1
  sourceRow:number
  sourceColumn:number
  rows:number
  columns:number
  cells:ClipboardCell[]
}

export type PastedCell = {
  row:number
  column:number
  value?:unknown
  formula?:string
  style?:Record<string,unknown>
}

function columnNumber(name:string){
  let value=0
  for(const character of name.toUpperCase())value=value*26+character.charCodeAt(0)-64
  return value
}

function columnName(column:number){
  let value=column,result=''
  while(value>0){value-=1;result=String.fromCharCode(65+value%26)+result;value=Math.floor(value/26)}
  return result
}

function shiftFormulaSegment(segment:string,rowDelta:number,columnDelta:number){
  return segment.replace(/(^|[^A-Za-z0-9_.])(\$?)([A-Za-z]{1,3})(\$?)([1-9]\d*)(?![A-Za-z0-9_])/g,(
    _match:string,prefix:string,columnAbsolute:string,columnLetters:string,rowAbsolute:string,rowDigits:string,
  )=>{
    const column=columnNumber(columnLetters)+(columnAbsolute?0:columnDelta)
    const row=Number(rowDigits)+(rowAbsolute?0:rowDelta)
    if(column<1||row<1)return `${prefix}#REF!`
    return `${prefix}${columnAbsolute}${columnName(column)}${rowAbsolute}${row}`
  })
}

export function shiftFormula(formula:string,rowDelta:number,columnDelta:number){
  if(!formula||(!rowDelta&&!columnDelta))return formula
  let result='',segment='',inString=false
  const flush=()=>{result+=inString?segment:shiftFormulaSegment(segment,rowDelta,columnDelta);segment=''}
  for(let index=0;index<formula.length;index+=1){
    const character=formula[index]
    if(character==='"'){
      if(inString&&formula[index+1]==='"'){segment+='""';index+=1;continue}
      flush();result+='"';inString=!inString
    }else segment+=character
  }
  flush()
  return result
}

export function parseTabularText(text:string){
  const rows:string[][]=[]
  let row:string[]=[],value='',quoted=false
  for(let index=0;index<text.length;index+=1){
    const character=text[index]
    if(character==='"'){
      if(quoted&&text[index+1]==='"'){value+='"';index+=1}else quoted=!quoted
    }else if(character==='\t'&&!quoted){row.push(value);value=''}
    else if((character==='\n'||character==='\r')&&!quoted){
      if(character==='\r'&&text[index+1]==='\n')index+=1
      row.push(value);rows.push(row);row=[];value=''
    }else value+=character
  }
  row.push(value)
  if(row.length>1||row[0]!==''||rows.length===0)rows.push(row)
  return rows
}

export function parseClipboardPayload(raw:string|undefined):KanpicClipboard|undefined{
  if(!raw)return
  try{
    const value=JSON.parse(raw) as Partial<KanpicClipboard>
    if(value.version!==1||!Number.isInteger(value.sourceRow)||!Number.isInteger(value.sourceColumn)||!Number.isInteger(value.rows)||!Number.isInteger(value.columns)||!Array.isArray(value.cells))return
    if(value.sourceRow!<1||value.sourceColumn!<1)return
    if(value.rows!<1||value.columns!<1||value.rows!>MAX_PASTE_CELLS||value.columns!>MAX_PASTE_CELLS)return
    if(value.rows!*value.columns!>MAX_PASTE_CELLS)return
    if(value.cells.length!==value.rows!*value.columns!)return
    if(value.cells.some(cell=>!Number.isInteger(cell.rowOffset)||!Number.isInteger(cell.columnOffset)||cell.rowOffset<0||cell.columnOffset<0||cell.rowOffset>=value.rows!||cell.columnOffset>=value.columns!))return
    return value as KanpicClipboard
  }catch{return}
}

function parsedValue(raw:string):unknown{
  if(raw==='')return undefined
  if(raw.toLowerCase()==='true')return true
  if(raw.toLowerCase()==='false')return false
  if(Number.isFinite(Number(raw))&&raw.trim()!=='')return Number(raw)
  return raw
}

export function materializePaste(text:string,internalRaw:string|undefined,startRow:number,startColumn:number){
  const internal=parseClipboardPayload(internalRaw)
  if(internal){
    if(internal.rows*internal.columns>MAX_PASTE_CELLS)throw new Error(`붙여넣기는 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`)
    return validateGridBounds(internal.cells.map(cell=>({
      row:startRow+cell.rowOffset,
      column:startColumn+cell.columnOffset,
      value:cell.formula?undefined:cell.value,
      formula:cell.formula?shiftFormula(cell.formula,startRow-internal.sourceRow,startColumn-internal.sourceColumn):undefined,
      style:cell.style,
    })))
  }
  const rows=parseTabularText(text)
  const count=rows.reduce((total,row)=>total+row.length,0)
  if(count>MAX_PASTE_CELLS)throw new Error(`붙여넣기는 최대 ${MAX_PASTE_CELLS.toLocaleString()}셀까지 가능합니다.`)
  return validateGridBounds(rows.flatMap((row,rowOffset)=>row.map((raw,columnOffset)=>{
    const formula=raw.startsWith('=')?raw:undefined
    return {row:startRow+rowOffset,column:startColumn+columnOffset,value:formula?undefined:parsedValue(raw),formula}
  })))
}

function validateGridBounds(cells:PastedCell[]){
  if(cells.some(cell=>cell.row<1||cell.row>MAX_GRID_ROWS||cell.column<1||cell.column>MAX_GRID_COLUMNS))throw new Error(`붙여넣을 범위가 시트 한도(${MAX_GRID_ROWS.toLocaleString()}행 × ${MAX_GRID_COLUMNS.toLocaleString()}열)를 벗어났습니다.`)
  return cells
}

function quoteTSV(value:string){
  return /[\t\r\n"]/.test(value)?`"${value.replace(/"/g,'""')}"`:value
}

export function clipboardText(payload:KanpicClipboard){
  const byOffset=new Map(payload.cells.map(cell=>[`${cell.rowOffset}:${cell.columnOffset}`,cell]))
  const rows:string[]=[]
  for(let row=0;row<payload.rows;row+=1){
    const values:string[]=[]
    for(let column=0;column<payload.columns;column+=1){
      const cell=byOffset.get(`${row}:${column}`)
      const value=cell?.formula||(cell?.value==null?'':String(cell.value))
      values.push(quoteTSV(value))
    }
    rows.push(values.join('\t'))
  }
  return rows.join('\n')
}
