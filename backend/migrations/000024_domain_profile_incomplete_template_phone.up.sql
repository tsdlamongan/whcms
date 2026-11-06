-- HasRegistrantAddress now also requires phone (RDash rejects a registrant
-- contact with a blank "voice" field) - update the domain_profile_incomplete
-- copy seeded by 000023 to mention phone too. Forward-only and
-- non-destructive: each UPDATE is guarded by `updated_at = created_at`, so a
-- template an operator has already customized is left exactly as they saved
-- it.
UPDATE email_templates SET
    body_html = '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">Profil Anda perlu dilengkapi</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Halo {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#fdf1f0;border-left:4px solid #c43c35;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#7b2622;">Kami belum bisa memproses domain <strong>{{.Domain}}</strong> karena profil Anda belum memiliki alamat dan nomor telepon lengkap (alamat, kota, provinsi, kode pos, telepon) yang diwajibkan registrar.</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Lengkapi profil Anda agar kami dapat melanjutkan proses domain ini.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/account" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Lengkapi Profil</a></td></tr></table><p style="margin:18px 0 0px;font:400 13px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#8a8a8a;">Setelah profil dilengkapi, hubungi tim kami agar domain ini diproses ulang.</p>',
    body_text = 'Halo {{.Name}},

Kami belum bisa memproses domain {{.Domain}} karena profil Anda belum memiliki alamat dan nomor telepon lengkap (alamat, kota, provinsi, kode pos, telepon) yang diwajibkan registrar.

Lengkapi profil Anda di: {{.FrontendURL}}/account

Setelah itu, hubungi tim kami agar domain ini diproses ulang.

-- {{.CompanyName}}'
    WHERE key = 'domain_profile_incomplete' AND locale = 'id' AND updated_at = created_at;

UPDATE email_templates SET
    body_html = '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">Your profile needs a couple of details</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Hello {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#fdf1f0;border-left:4px solid #c43c35;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#7b2622;">We could not process the domain <strong>{{.Domain}}</strong> because your profile is missing the full address and phone number (address, city, state/province, postal code, phone) the registrar requires.</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Please complete your profile so we can continue processing this domain.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/account" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Complete Your Profile</a></td></tr></table><p style="margin:18px 0 0px;font:400 13px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#8a8a8a;">Once it is complete, please reach out to us so this domain can be retried.</p>',
    body_text = 'Hello {{.Name}},

We could not process the domain {{.Domain}} because your profile is missing the full address and phone number (address, city, state/province, postal code, phone) the registrar requires.

Complete your profile at: {{.FrontendURL}}/account

Once done, please reach out to us so this domain can be retried.

-- {{.CompanyName}}'
    WHERE key = 'domain_profile_incomplete' AND locale = 'en' AND updated_at = created_at;
