/**
 * Domain document / verification rules, transcribed from the registrar's
 * documentation (docs.dewabiz.co.id: "Syarat Domain Indonesia", "Auto
 * Activation"). Mirrors backend `internal/domain/documents.go` - change them
 * together. The backend is the source of truth for a registered domain
 * (`document_required`, `verification_status`, `verification_deadlines`); this
 * table only drives pre-purchase hints and the per-extension requirement list.
 */

/** Extensions that need identity/legal documents after registration. */
export const DOCUMENT_TLDS = ['co.id', 'or.id', 'ac.id', 'sch.id', 'ponpes.id'] as const;
export type DocumentTld = (typeof DOCUMENT_TLDS)[number];

/** Extensions verified through the registrant's email only (30 days, then suspended). */
export const EMAIL_VERIFICATION_TLDS = ['id', 'my.id', 'biz.id', 'web.id'] as const;

/**
 * i18n key suffixes (`clientDomains.docs.req.<key>`) of the documents each
 * extension requires. The first entry is always the identity document.
 */
export const DOCUMENT_REQUIREMENTS: Record<DocumentTld, readonly string[]> = {
	'co.id': ['identity', 'companyLegal', 'intlBrand'],
	'or.id': ['identity', 'orgDeed', 'intlBrand'],
	'ac.id': ['identity', 'highEdDecree', 'requestLetterRector'],
	'sch.id': ['identity', 'requestLetterHead', 'schoolPermit', 'nameMatchesInstitution'],
	'ponpes.id': ['identity', 'requestLetterPesantren', 'nameMatchesInstitution']
};

/** Everything after the first label: "toko.co.id" -> "co.id". */
export function tldOf(name: string): string {
	const n = name.trim().toLowerCase().replace(/\.$/, '');
	const i = n.indexOf('.');
	return i < 0 ? '' : n.slice(i + 1);
}

function isDocumentTld(tld: string): tld is DocumentTld {
	return (DOCUMENT_TLDS as readonly string[]).includes(tld);
}

/** The extension of `name` needs documents ("sekolah.sch.id"). */
export function requiresDocuments(name: string): boolean {
	return isDocumentTld(tldOf(name));
}

/** The extension of `name` is verified through the registrant's email. */
export function requiresEmailVerification(name: string): boolean {
	return (EMAIL_VERIFICATION_TLDS as readonly string[]).includes(tldOf(name));
}

/** i18n requirement keys for `name`'s extension (empty when none are needed). */
export function documentRequirementKeys(name: string): readonly string[] {
	const tld = tldOf(name);
	return isDocumentTld(tld) ? DOCUMENT_REQUIREMENTS[tld] : [];
}
