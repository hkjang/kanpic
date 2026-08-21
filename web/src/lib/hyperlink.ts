import type { Cell } from '../types'

export type CellLink={href:string;label:string;internal:boolean}

const SAFE_SCHEMES=['http://','https://','mailto:']

/**
 * A link is only followed if it names a scheme we are willing to open. Cell
 * text arrives from imports, pastes and other people's workbooks, so a
 * `javascript:` target is a real possibility rather than a hypothetical one.
 */
export function safeLinkTarget(value:string):string|undefined{
  const target=value.trim()
  if(target==='')return undefined
  const lower=target.toLowerCase()
  if(target.startsWith('/'))return target
  return SAFE_SCHEMES.some(scheme=>lower.startsWith(scheme))?target:undefined
}

/** Whether the target points back into this kanpic instance. */
export function internalLink(href:string){
  if(href.startsWith('/'))return true
  try{return new URL(href,window.location.origin).origin===window.location.origin}
  catch{return false}
}

// =HYPERLINK("주소") or =HYPERLINK("주소","표시 텍스트"), which is what the
// link dialog writes. Anything more nested is left as an ordinary formula:
// guessing at the target of =IF(x,HYPERLINK(a),HYPERLINK(b)) would be wrong
// half the time.
const HYPERLINK=/^=\s*HYPERLINK\s*\(\s*"((?:[^"]|"")*)"\s*(?:,\s*"((?:[^"]|"")*)"\s*)?\)\s*$/i
const BARE_URL=/^(?:https?:\/\/|mailto:)\S+$/i

const unquote=(value:string)=>value.replace(/""/g,'"')

/**
 * The link a cell carries, if any: written with HYPERLINK, or simply a URL
 * typed into the cell the way people paste one.
 */
export function cellLink(cell:Cell|undefined):CellLink|undefined{
  if(!cell)return undefined
  if(cell.formula){
    const matched=HYPERLINK.exec(cell.formula.trim())
    if(!matched)return undefined
    const href=safeLinkTarget(unquote(matched[1]))
    if(!href)return undefined
    const label=matched[2]!==undefined?unquote(matched[2]):(cell.value==null?href:String(cell.value))
    return {href,label:label||href,internal:internalLink(href)}
  }
  if(typeof cell.value!=='string'||!BARE_URL.test(cell.value.trim()))return undefined
  const href=safeLinkTarget(cell.value)
  return href?{href,label:cell.value.trim(),internal:internalLink(href)}:undefined
}
