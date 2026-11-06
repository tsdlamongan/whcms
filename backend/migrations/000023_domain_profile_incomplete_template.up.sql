-- New domain_profile_incomplete email template (id + en): sent when a
-- domain register/transfer job fails because the client's profile is
-- missing required registrant address details (address/city/state/
-- postcode) - see domains.Service.registrantContact. Uses the same
-- redesigned inline-style markup as migration 000020 so it renders
-- correctly through the global "_layout" wrapper.
INSERT INTO email_templates (key, locale, subject, body_html, body_text) VALUES
('domain_profile_incomplete', 'id', 'Tindakan diperlukan: lengkapi profil untuk domain {{.Domain}}',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">Profil Anda perlu dilengkapi</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Halo {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#fdf1f0;border-left:4px solid #c43c35;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#7b2622;">Kami belum bisa memproses domain <strong>{{.Domain}}</strong> karena profil Anda belum memiliki alamat lengkap (alamat, kota, provinsi, kode pos) yang diwajibkan registrar.</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Lengkapi profil Anda agar kami dapat melanjutkan proses domain ini.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/account" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Lengkapi Profil</a></td></tr></table><p style="margin:18px 0 0px;font:400 13px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#8a8a8a;">Setelah profil dilengkapi, hubungi tim kami agar domain ini diproses ulang.</p>',
 'Halo {{.Name}},

Kami belum bisa memproses domain {{.Domain}} karena profil Anda belum memiliki alamat lengkap (alamat, kota, provinsi, kode pos) yang diwajibkan registrar.

Lengkapi profil Anda di: {{.FrontendURL}}/account

Setelah itu, hubungi tim kami agar domain ini diproses ulang.

-- {{.CompanyName}}'),
('domain_profile_incomplete', 'en', 'Action needed: complete your profile for domain {{.Domain}}',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">Your profile needs a couple of details</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Hello {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#fdf1f0;border-left:4px solid #c43c35;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#7b2622;">We could not process the domain <strong>{{.Domain}}</strong> because your profile is missing the full address (address, city, state/province, postal code) the registrar requires.</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Please complete your profile so we can continue processing this domain.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/account" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Complete Your Profile</a></td></tr></table><p style="margin:18px 0 0px;font:400 13px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#8a8a8a;">Once it is complete, please reach out to us so this domain can be retried.</p>',
 'Hello {{.Name}},

We could not process the domain {{.Domain}} because your profile is missing the full address (address, city, state/province, postal code) the registrar requires.

Complete your profile at: {{.FrontendURL}}/account

Once done, please reach out to us so this domain can be retried.

-- {{.CompanyName}}')
ON CONFLICT (key, locale) DO NOTHING;
